/*
Copyright 2014 Hajime Hoshi

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package ebiten

import (
	"github.com/hajimehoshi/ebiten/internal/opengl"
)

// A Rect represents a rectangle.
type Rect struct {
	X      float64
	Y      float64
	Width  float64
	Height float64
}

// An ImagePart represents a part of an image.
type ImagePart struct {
	Dst Rect
	Src Rect
}

// DrawWholeImage draws the whole image.
func DrawWholeImage(r *RenderTarget, image *Image, geo GeometryMatrix, color ColorMatrix) error {
	w, h := image.Size()
	parts := []ImagePart{
		{Rect{0, 0, float64(w), float64(h)}, Rect{0, 0, float64(w), float64(h)}},
	}
	return r.DrawImage(image, parts, geo, color)
}

// Filter represents the type of filter to be used when an image is maginified or minified.
type Filter int

// Filters
const (
	FilterNearest Filter = iota
	FilterLinear
)

// An Image represents an image to be rendered.
type Image struct {
	glTexture *opengl.Texture
}

// Size returns the size of the image.
func (i *Image) Size() (width int, height int) {
	return i.glTexture.Size()
}
