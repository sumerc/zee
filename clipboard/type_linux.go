//go:build linux && !nativeclipboard

package clipboard

// a=30, b=48, c=46, d=32, e=18, f=33, g=34, h=35, i=23, j=36,
// k=37, l=38, m=50, n=49, o=24, p=25, q=16, r=19, s=31, t=20,
// u=22, v=47, w=17, x=45, y=21, z=44
var keymap = [26]uint16{
	30, 48, 46, 32, 18, 33, 34, 35, 23, 36,
	37, 38, 50, 49, 24, 25, 16, 19, 31, 20,
	22, 47, 17, 45, 21, 44,
}

// 0=11, 1=2, 2=3, ..., 9=10
var nummap = [10]uint16{11, 2, 3, 4, 5, 6, 7, 8, 9, 10}

func charToKey(c byte) (code uint16, shift bool, ok bool) {
	switch {
	case c >= 'a' && c <= 'z':
		return keymap[c-'a'], false, true
	case c >= 'A' && c <= 'Z':
		return keymap[c-'A'], true, true
	case c >= '0' && c <= '9':
		return nummap[c-'0'], false, true
	case c == ' ':
		return 57, false, true // KEY_SPACE
	case c == '\n':
		return 28, false, true // KEY_ENTER
	case c == '\t':
		return 15, false, true // KEY_TAB
	default:
		return punctKey(c)
	}
}

func punctKey(c byte) (uint16, bool, bool) {
	type km struct {
		code  uint16
		shift bool
	}
	m := map[byte]km{
		'.': {52, false}, ',': {51, false}, '/': {53, false},
		';': {39, false}, '\'': {40, false}, '[': {26, false},
		']': {27, false}, '-': {12, false}, '=': {13, false},
		'\\': {43, false}, '`': {41, false},
		'!': {2, true}, '@': {3, true}, '#': {4, true},
		'$': {5, true}, '%': {6, true}, '^': {7, true},
		'&': {8, true}, '*': {9, true}, '(': {10, true},
		')': {11, true}, '_': {12, true}, '+': {13, true},
		'{': {26, true}, '}': {27, true}, '|': {43, true},
		':': {39, true}, '"': {40, true}, '<': {51, true},
		'>': {52, true}, '?': {53, true}, '~': {41, true},
	}
	if k, ok := m[c]; ok {
		return k.code, k.shift, true
	}
	return 0, false, false
}

// Type sends each character of text as a keystroke via uinput.
func Type(text string) error {
	if err := Init(); err != nil {
		return err
	}
	for i := 0; i < len(text); i++ {
		code, shift, ok := charToKey(text[i])
		if !ok {
			continue // skip unsupported characters
		}
		if err := keyTap(code, shift); err != nil {
			return err
		}
	}
	return nil
}
