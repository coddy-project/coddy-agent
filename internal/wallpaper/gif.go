package wallpaper

import "encoding/binary"

// checkGIFBudget walks the container without decoding pixels. DecodeAll keeps
// every frame, so both cumulative pixels and frame count must be bounded before
// it allocates. DecodeAll then validates the palette, compressed data and bounds.
func checkGIFBudget(raw []byte) error {
	if len(raw) < 13 {
		return ErrInvalidImage
	}
	pos := 13
	skip := func(n int) bool {
		if n < 0 || n > len(raw)-pos {
			return false
		}
		pos += n
		return true
	}
	if raw[10]&0x80 != 0 && !skip(3<<(uint(raw[10]&7)+1)) {
		return ErrInvalidImage
	}
	subBlocks := func() bool {
		for pos < len(raw) {
			size := int(raw[pos])
			pos++
			if size == 0 {
				return true
			}
			if !skip(size) {
				return false
			}
		}
		return false
	}
	pixels, frames := 0, 0
	for pos < len(raw) {
		kind := raw[pos]
		pos++
		switch kind {
		case 0x3b:
			if frames == 0 {
				return ErrInvalidImage
			}
			return nil
		case 0x21:
			if !skip(1) || !subBlocks() {
				return ErrInvalidImage
			}
		case 0x2c:
			if len(raw)-pos < 9 {
				return ErrInvalidImage
			}
			w := int(binary.LittleEndian.Uint16(raw[pos+4 : pos+6]))
			h := int(binary.LittleEndian.Uint16(raw[pos+6 : pos+8]))
			flags := raw[pos+8]
			pos += 9
			frames++
			if w == 0 || h == 0 || frames > 256 || w > (MaxPixels-pixels)/h {
				return ErrInvalidImage
			}
			pixels += w * h
			if flags&0x80 != 0 && !skip(3<<(uint(flags&7)+1)) {
				return ErrInvalidImage
			}
			if !skip(1) || !subBlocks() {
				return ErrInvalidImage
			}
		default:
			return ErrInvalidImage
		}
	}
	return ErrInvalidImage
}
