package view

import (
	"image"
	"image/draw"
	"sort"
)

var sb = &SpriteBatch{}

type drawSprite struct {
	f     func()
	layer float32
	y     int
}

type SpriteBatch struct {
	batch []drawSprite
}

func (s *SpriteBatch) Len() int { return len(s.batch) }
func (s *SpriteBatch) Less(i, j int) bool {
	if s.batch[i].layer == s.batch[j].layer {
		return s.batch[i].y < s.batch[j].y
	}
	return s.batch[i].layer < s.batch[j].layer
}
func (s *SpriteBatch) Swap(i, j int) {
	s.batch[i], s.batch[j] = s.batch[j], s.batch[i]
}

func (s *SpriteBatch) Start() {
	s.batch = s.batch[:0] // reslice. Avoids re-allocating but also keeps the underlying data around.
}

func (s *SpriteBatch) DrawMask(dst draw.Image, r image.Rectangle, src image.Image, sp image.Point, mask image.Image, mp image.Point, op draw.Op, layer float32) {
	s.DrawMask(dst, r, src, sp, mask, sp, draw.Over, layer)
}

func (s *SpriteBatch) Draw(dst draw.Image, r image.Rectangle, src image.Image, sp image.Point, layer float32) {
	s.batch = append(s.batch, drawSprite{
		layer: layer,
		f: func() {
			draw.DrawMask(dst, r, src, sp, mask, sp, draw.Over)
		},
		y: r.Max.Y,
	})
}

func (s *SpriteBatch) Flush() {
	sort.Stable(s)
	for _, sprite := range s.batch {
		sprite.f()
	}
}
