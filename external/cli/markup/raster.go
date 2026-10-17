//go:build cli

package markup

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	"golang.org/x/image/vector"
)

// Pt is a point in floating pixel coordinates.
type Pt struct{ X, Y float64 }

func (p Pt) add(q Pt) Pt        { return Pt{p.X + q.X, p.Y + q.Y} }
func (p Pt) sub(q Pt) Pt        { return Pt{p.X - q.X, p.Y - q.Y} }
func (p Pt) mul(k float64) Pt   { return Pt{p.X * k, p.Y * k} }
func (p Pt) len() float64       { return math.Hypot(p.X, p.Y) }
func (p Pt) dist(q Pt) float64  { return p.sub(q).len() }
func ptOf(p image.Point) Pt     { return Pt{float64(p.X), float64(p.Y)} }
func (p Pt) round() image.Point { return image.Pt(int(math.Round(p.X)), int(math.Round(p.Y))) }
func (p Pt) in(r image.Rectangle) bool {
	return p.X >= float64(r.Min.X) && p.X < float64(r.Max.X) && p.Y >= float64(r.Min.Y) && p.Y < float64(r.Max.Y)
}

// polygon is a closed path. Every polygon of one fill is turned to the same
// orientation before it is rasterised, except the holes of a ring, which are
// turned the other way: the rasteriser sums signed coverage and clamps its
// magnitude, so polygons of one sign make a union and an opposite one cuts.
type polygon []Pt

func (p polygon) area() float64 {
	var a float64
	for i := range p {
		q := p[(i+1)%len(p)]
		a += p[i].X*q.Y - q.X*p[i].Y
	}
	return a / 2
}

func (p polygon) oriented(positive bool) polygon {
	if (p.area() >= 0) == positive {
		return p
	}
	out := make(polygon, len(p))
	for i := range p {
		out[i] = p[len(p)-1-i]
	}
	return out
}

// fill paints the union of the solid polygons minus the holes onto dst in c,
// antialiased, blending over what is there.
func fill(dst draw.Image, solids, holes []polygon, c color.Color) {
	box := image.Rectangle{}
	first := true
	for _, set := range [][]polygon{solids, holes} {
		for _, poly := range set {
			for _, p := range poly {
				r := image.Rect(int(math.Floor(p.X))-1, int(math.Floor(p.Y))-1, int(math.Ceil(p.X))+1, int(math.Ceil(p.Y))+1)
				if first {
					box, first = r, false
				} else {
					box = box.Union(r)
				}
			}
		}
	}
	box = box.Intersect(dst.Bounds())
	if box.Empty() {
		return
	}
	r := vector.NewRasterizer(box.Dx(), box.Dy())
	add := func(poly polygon) {
		if len(poly) < 3 {
			return
		}
		ox, oy := float64(box.Min.X), float64(box.Min.Y)
		r.MoveTo(float32(poly[0].X-ox), float32(poly[0].Y-oy))
		for _, p := range poly[1:] {
			r.LineTo(float32(p.X-ox), float32(p.Y-oy))
		}
		r.ClosePath()
	}
	for _, poly := range solids {
		add(poly.oriented(true))
	}
	for _, poly := range holes {
		add(poly.oriented(false))
	}
	r.Draw(dst, box, image.NewUniform(c), image.Point{})
}

// circlePoly approximates a circle closely enough for any radius on screen.
func circlePoly(c Pt, r float64) polygon {
	return ellipsePoly(c, r, r)
}

func ellipsePoly(c Pt, rx, ry float64) polygon {
	n := int(math.Max(12, math.Min(180, (rx+ry)*0.6)))
	out := make(polygon, n)
	for i := 0; i < n; i++ {
		a := 2 * math.Pi * float64(i) / float64(n)
		out[i] = Pt{c.X + rx*math.Cos(a), c.Y + ry*math.Sin(a)}
	}
	return out
}

func rectPoly(r image.Rectangle) polygon {
	return rectPolyF(ptOf(r.Min), ptOf(r.Max))
}

func rectPolyF(a, b Pt) polygon {
	return polygon{{a.X, a.Y}, {b.X, a.Y}, {b.X, b.Y}, {a.X, b.Y}}
}

// strokePolys is a polyline of the given width with round joins and caps.
func strokePolys(pts []Pt, width float64) []polygon {
	h := width / 2
	if len(pts) == 0 || h <= 0 {
		return nil
	}
	out := []polygon{circlePoly(pts[0], h)}
	for i := 1; i < len(pts); i++ {
		a, b := pts[i-1], pts[i]
		d := b.sub(a)
		l := d.len()
		if l < 1e-6 {
			continue
		}
		n := Pt{-d.Y / l * h, d.X / l * h}
		out = append(out, polygon{a.add(n), b.add(n), b.sub(n), a.sub(n)}, circlePoly(b, h))
	}
	return out
}

// strokeLine draws a polyline.
func strokeLine(dst draw.Image, pts []Pt, width float64, c color.Color) {
	fill(dst, strokePolys(pts, width), nil, c)
}

// normRect orders two corners into min and max.
func normRect(a, b Pt) (Pt, Pt) {
	return Pt{math.Min(a.X, b.X), math.Min(a.Y, b.Y)}, Pt{math.Max(a.X, b.X), math.Max(a.Y, b.Y)}
}

// strokeEllipse draws the outline of the ellipse inscribed in the box a-b.
func strokeEllipse(dst draw.Image, a, b Pt, width float64, c color.Color) {
	lo, hi := normRect(a, b)
	center := Pt{(lo.X + hi.X) / 2, (lo.Y + hi.Y) / 2}
	rx, ry := (hi.X-lo.X)/2, (hi.Y-lo.Y)/2
	h := width / 2
	outer := ellipsePoly(center, rx+h, ry+h)
	if rx-h <= 0 || ry-h <= 0 {
		fill(dst, []polygon{outer}, nil, c)
		return
	}
	fill(dst, []polygon{outer}, []polygon{ellipsePoly(center, rx-h, ry-h)}, c)
}

// strokeRect draws the outline of the box a-b with square corners.
func strokeRect(dst draw.Image, a, b Pt, width float64, c color.Color) {
	lo, hi := normRect(a, b)
	h := width / 2
	outer := rectPolyF(Pt{lo.X - h, lo.Y - h}, Pt{hi.X + h, hi.Y + h})
	if hi.X-lo.X <= width || hi.Y-lo.Y <= width {
		fill(dst, []polygon{outer}, nil, c)
		return
	}
	fill(dst, []polygon{outer}, []polygon{rectPolyF(Pt{lo.X + h, lo.Y + h}, Pt{hi.X - h, hi.Y - h})}, c)
}

// arrowPolys is a shaft from a to b with a filled head at b.
func arrowPolys(a, b Pt, width float64) []polygon {
	d := b.sub(a)
	l := d.len()
	if l < 1e-6 {
		return []polygon{circlePoly(a, width/2)}
	}
	u := d.mul(1 / l)
	n := Pt{-u.Y, u.X}
	head := math.Min(width*4+6, l*0.6)
	half := head * 0.55
	base := b.sub(u.mul(head))
	// The shaft stops inside the head so its round cap never pokes out of
	// the tip.
	shaftEnd := b.sub(u.mul(head * 0.5))
	out := strokePolys([]Pt{a, shaftEnd}, width)
	return append(out, polygon{b, base.add(n.mul(half)), base.sub(n.mul(half))})
}

func drawArrow(dst draw.Image, a, b Pt, width float64, c color.Color) {
	fill(dst, arrowPolys(a, b, width), nil, c)
}

// roundRectPoly is a rectangle with corners of radius rad.
func roundRectPoly(r image.Rectangle, rad float64) polygon {
	lo, hi := ptOf(r.Min), ptOf(r.Max)
	rad = math.Min(rad, math.Min((hi.X-lo.X)/2, (hi.Y-lo.Y)/2))
	if rad <= 0 {
		return rectPoly(r)
	}
	var out polygon
	corner := func(c Pt, from float64) {
		const steps = 8
		for i := 0; i <= steps; i++ {
			a := from + math.Pi/2*float64(i)/steps
			out = append(out, Pt{c.X + rad*math.Cos(a), c.Y + rad*math.Sin(a)})
		}
	}
	corner(Pt{hi.X - rad, lo.Y + rad}, -math.Pi/2)
	corner(Pt{hi.X - rad, hi.Y - rad}, 0)
	corner(Pt{lo.X + rad, hi.Y - rad}, math.Pi/2)
	corner(Pt{lo.X + rad, lo.Y + rad}, math.Pi)
	return out
}

func fillRoundRect(dst draw.Image, r image.Rectangle, rad float64, c color.Color) {
	fill(dst, []polygon{roundRectPoly(r, rad)}, nil, c)
}

func fillCircle(dst draw.Image, c Pt, r float64, col color.Color) {
	fill(dst, []polygon{circlePoly(c, r)}, nil, col)
}

func strokeCircle(dst draw.Image, c Pt, r, width float64, col color.Color) {
	strokeEllipse(dst, Pt{c.X - r, c.Y - r}, Pt{c.X + r, c.Y + r}, width, col)
}

// arcPoints samples an arc of radius r around c from angle a0 to a1.
func arcPoints(c Pt, r, a0, a1 float64) []Pt {
	n := int(math.Max(6, math.Abs(a1-a0)*r/2))
	out := make([]Pt, n+1)
	for i := 0; i <= n; i++ {
		a := a0 + (a1-a0)*float64(i)/float64(n)
		out[i] = Pt{c.X + r*math.Cos(a), c.Y + r*math.Sin(a)}
	}
	return out
}
