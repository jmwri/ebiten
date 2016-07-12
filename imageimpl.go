// Copyright 2016 Hajime Hoshi
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ebiten

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"runtime"
	"sync"

	"github.com/hajimehoshi/ebiten/internal/graphics"
	"github.com/hajimehoshi/ebiten/internal/graphics/opengl"
	"github.com/hajimehoshi/ebiten/internal/loop"
	"github.com/hajimehoshi/ebiten/internal/ui"
)

type drawImageHistoryItem struct {
	image    *Image
	vertices []int16
	geom     GeoM
	colorm   ColorM
	mode     opengl.CompositeMode
}

type imageImpl struct {
	image            *graphics.Image
	disposed         bool
	width            int
	height           int
	filter           Filter
	pixels           []uint8
	baseColor        color.Color
	drawImageHistory []*drawImageHistoryItem
	volatile         bool
	screen           bool
	m                sync.Mutex
}

func newImageImpl(width, height int, filter Filter, volatile bool) (*imageImpl, error) {
	img, err := graphics.NewImage(width, height, glFilter(filter))
	if err != nil {
		return nil, err
	}
	i := &imageImpl{
		image:    img,
		width:    width,
		height:   height,
		filter:   filter,
		volatile: volatile,
		pixels:   make([]uint8, width*height*4),
	}
	runtime.SetFinalizer(i, (*imageImpl).Dispose)
	return i, nil
}

func newImageImplFromImage(source image.Image, filter Filter) (*imageImpl, error) {
	size := source.Bounds().Size()
	w, h := size.X, size.Y
	// TODO: Return error when the image is too big!
	// Don't lock while manipulating an image.Image interface.
	rgbaImg, ok := source.(*image.RGBA)
	if !ok || source.Bounds().Min != image.ZP {
		origImg := source
		newImg := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.Draw(newImg, newImg.Bounds(), origImg, origImg.Bounds().Min, draw.Src)
		rgbaImg = newImg
	}
	pixels := make([]uint8, 4*w*h)
	for j := 0; j < h; j++ {
		copy(pixels[j*w*4:(j+1)*w*4], rgbaImg.Pix[j*rgbaImg.Stride:])
	}
	img, err := graphics.NewImageFromImage(rgbaImg, glFilter(filter))
	if err != nil {
		// TODO: texture should be removed here?
		return nil, err
	}
	i := &imageImpl{
		image:  img,
		width:  w,
		height: h,
		filter: filter,
		pixels: pixels,
	}
	runtime.SetFinalizer(i, (*imageImpl).Dispose)
	return i, nil
}

func newScreenImageImpl(width, height int) (*imageImpl, error) {
	img, err := graphics.NewScreenFramebufferImage(width, height)
	if err != nil {
		return nil, err
	}
	i := &imageImpl{
		image:    img,
		width:    width,
		height:   height,
		volatile: true,
		screen:   true,
		pixels:   make([]uint8, width*height*4),
	}
	runtime.SetFinalizer(i, (*imageImpl).Dispose)
	return i, nil
}

func (i *imageImpl) Fill(clr color.Color) error {
	i.m.Lock()
	defer i.m.Unlock()
	if i.disposed {
		return errors.New("ebiten: image is already disposed")
	}
	i.pixels = nil
	i.baseColor = clr
	i.drawImageHistory = nil
	return i.image.Fill(clr)
}

func (i *imageImpl) clearIfVolatile() error {
	i.m.Lock()
	defer i.m.Unlock()
	if i.disposed {
		return nil
	}
	if !i.volatile {
		return nil
	}
	i.pixels = nil
	i.baseColor = nil
	i.drawImageHistory = nil
	return i.image.Fill(color.Transparent)
}

func (i *imageImpl) DrawImage(image *Image, options *DrawImageOptions) error {
	// Calculate vertices before locking because the user can do anything in
	// options.ImageParts interface without deadlock (e.g. Call Image functions).
	if options == nil {
		options = &DrawImageOptions{}
	}
	parts := options.ImageParts
	if parts == nil {
		// Check options.Parts for backward-compatibility.
		dparts := options.Parts
		if dparts != nil {
			parts = imageParts(dparts)
		} else {
			parts = &wholeImage{image.impl.width, image.impl.height}
		}
	}
	quads := &textureQuads{parts: parts, width: image.impl.width, height: image.impl.height}
	// TODO: Reuse one vertices instead of making here, but this would need locking.
	vertices := make([]int16, parts.Len()*16)
	n := quads.vertices(vertices)
	if n == 0 {
		return nil
	}
	if i == image.impl {
		return errors.New("ebiten: Image.DrawImage: image should be different from the receiver")
	}
	i.m.Lock()
	defer i.m.Unlock()
	if i.disposed {
		return errors.New("ebiten: image is already disposed")
	}
	c := &drawImageHistoryItem{
		image:    image,
		vertices: vertices,
		geom:     options.GeoM,
		colorm:   options.ColorM,
		mode:     opengl.CompositeMode(options.CompositeMode),
	}
	i.drawImageHistory = append(i.drawImageHistory, c)
	geom := &options.GeoM
	colorm := &options.ColorM
	mode := opengl.CompositeMode(options.CompositeMode)
	if err := i.image.DrawImage(image.impl.image, vertices, geom, colorm, mode); err != nil {
		return err
	}
	return nil
}

func (i *imageImpl) At(x, y int) color.Color {
	if !loop.IsRunning() {
		panic("ebiten: At can't be called when the GL context is not initialized (this panic happens as of version 1.4.0-alpha)")
	}
	i.m.Lock()
	defer i.m.Unlock()
	if i.disposed {
		return color.Transparent
	}
	if i.pixels == nil || i.drawImageHistory != nil {
		var err error
		i.pixels, err = i.image.Pixels(ui.GLContext())
		if err != nil {
			panic(err)
		}
		i.drawImageHistory = nil
	}
	idx := 4*x + 4*y*i.width
	r, g, b, a := i.pixels[idx], i.pixels[idx+1], i.pixels[idx+2], i.pixels[idx+3]
	return color.RGBA{r, g, b, a}
}

func (i *imageImpl) hasHistoryWith(target *Image) bool {
	for _, c := range i.drawImageHistory {
		if c.image == target {
			return true
		}
	}
	return false
}

func (i *imageImpl) resetHistoryIfNeeded(target *Image) error {
	i.m.Lock()
	defer i.m.Unlock()
	if i.disposed {
		return nil
	}
	if i.drawImageHistory == nil {
		return nil
	}
	if !i.hasHistoryWith(target) {
		return nil
	}
	var err error
	i.pixels, err = i.image.Pixels(ui.GLContext())
	if err != nil {
		return nil
	}
	i.baseColor = nil
	i.drawImageHistory = nil
	return nil
}

func (i *imageImpl) hasHistory() bool {
	i.m.Lock()
	defer i.m.Unlock()
	return i.drawImageHistory != nil
}

func (i *imageImpl) restore(context *opengl.Context) error {
	i.m.Lock()
	defer i.m.Unlock()
	if i.disposed {
		return nil
	}
	if i.screen {
		// The screen image should also be recreated because framebuffer might
		// be changed.
		var err error
		i.image, err = graphics.NewScreenFramebufferImage(i.width, i.height)
		if err != nil {
			return err
		}
		return nil
	}
	if !i.volatile {
		img := image.NewRGBA(image.Rect(0, 0, i.width, i.height))
		if i.pixels != nil {
			for j := 0; j < i.height; j++ {
				copy(img.Pix[j*img.Stride:], i.pixels[j*i.width*4:(j+1)*i.width*4])
			}
		} else if i.baseColor != nil {
			r32, g32, b32, a32 := i.baseColor.RGBA()
			r, g, b, a := uint8(r32), uint8(g32), uint8(b32), uint8(a32)
			for idx := 0; idx < len(img.Pix)/4; idx++ {
				img.Pix[4*idx] = r
				img.Pix[4*idx+1] = g
				img.Pix[4*idx+2] = b
				img.Pix[4*idx+3] = a
			}
		}
		var err error
		i.image, err = graphics.NewImageFromImage(img, glFilter(i.filter))
		if err != nil {
			return err
		}
		for _, c := range i.drawImageHistory {
			if c.image.impl.hasHistory() {
				panic("not reach")
			}
			if err := i.image.DrawImage(c.image.impl.image, c.vertices, &c.geom, &c.colorm, c.mode); err != nil {
				return err
			}
		}
		if 0 < len(i.drawImageHistory) {
			i.pixels, err = i.image.Pixels(context)
			if err != nil {
				return err
			}
		}
		i.baseColor = nil
		i.drawImageHistory = nil
		return nil
	}
	var err error
	i.image, err = graphics.NewImage(i.width, i.height, glFilter(i.filter))
	if err != nil {
		return err
	}
	return nil
}

func (i *imageImpl) Dispose() error {
	i.m.Lock()
	defer i.m.Unlock()
	if i.disposed {
		return errors.New("ebiten: image is already disposed")
	}
	if !i.screen {
		if err := i.image.Dispose(); err != nil {
			return err
		}
	}
	i.image = nil
	i.disposed = true
	i.pixels = nil
	i.baseColor = nil
	i.drawImageHistory = nil
	runtime.SetFinalizer(i, nil)
	return nil
}

func (i *imageImpl) ReplacePixels(p []uint8) error {
	if l := 4 * i.width * i.height; len(p) != l {
		return fmt.Errorf("ebiten: p's length must be %d", l)
	}
	i.m.Lock()
	defer i.m.Unlock()
	if i.pixels == nil {
		i.pixels = make([]uint8, i.width*i.height*4)
	}
	copy(i.pixels, p)
	i.baseColor = nil
	i.drawImageHistory = nil
	if i.disposed {
		return errors.New("ebiten: image is already disposed")
	}
	return i.image.ReplacePixels(p)
}

func (i *imageImpl) isDisposed() bool {
	i.m.Lock()
	defer i.m.Unlock()
	return i.disposed
}

func (i *imageImpl) isInvalidated(context *opengl.Context) bool {
	i.m.Lock()
	defer i.m.Unlock()
	return i.image.IsInvalidated(context)
}
