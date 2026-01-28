package mp3

import (
	"math"
)

// subbandInitialize calculates the analysis filterbank coefficients and rounds to the 9th decimal
// place accuracy of the filterbank tables in the ISO document. The coefficients are stored in #filter#
func (enc *Encoder) subbandInitialize() {
	var (
		i      int64
		j      int64
		filter float64
	)
	for i = MAX_CHANNELS; func() int64 {
		p := &i
		x := *p
		*p--
		return x
	}() != 0; {
		enc.subband.Off[i] = 0
		enc.subband.X[i] = [HAN_SIZE]int32{}
	}
	for i = SUBBAND_LIMIT; func() int64 {
		p := &i
		x := *p
		*p--
		return x
	}() != 0; {
		for j = 64; func() int64 {
			p := &j
			x := *p
			*p--
			return x
		}() != 0; {
			if (func() float64 {
				filter = math.Cos(float64((i*2+1)*(16-j))*PI64) * 1e+09
				return filter
			}()) >= 0 {
				filter, _ = math.Modf(filter + 0.5)
			} else {
				filter, _ = math.Modf(filter - 0.5)
			}
			enc.subband.Fl[i][j] = int32(filter * (math.MaxInt32 * 1e-09))
		}
	}
}

// windowFilterSubband processes samples from the buffer at the given position.
// Returns the new buffer position after reading 32 samples.
func (enc *Encoder) windowFilterSubband(bufPos int, s *[32]int32, ch int64) int {
	var (
		y [64]int32
		i int64
		j int64
	)

	stride := enc.bufferStride

	// Read 32 samples with stride
	for i = 32; func() int64 {
		p := &i
		x := *p
		*p--
		return x
	}() != 0; {
		if bufPos < len(enc.buffer) {
			enc.subband.X[ch][i+enc.subband.Off[ch]] = int32(int64(int32(enc.buffer[bufPos])) << 16)
		}
		bufPos += stride
	}

	for i = 64; func() int64 {
		p := &i
		x := *p
		*p--
		return x
	}() != 0; {
		var (
			s_value    int32
			s_value_lo uint32
		)
		_ = s_value_lo
		s_value = int32(((int64(enc.subband.X[ch][(enc.subband.Off[ch]+i+(0<<6))&(HAN_SIZE-1)])) * (int64(enWindow[i+(0<<6)]))) >> 32)
		s_value += int32(((int64(enc.subband.X[ch][(enc.subband.Off[ch]+i+(1<<6))&(HAN_SIZE-1)])) * (int64(enWindow[i+(1<<6)]))) >> 32)
		s_value += int32(((int64(enc.subband.X[ch][(enc.subband.Off[ch]+i+(2<<6))&(HAN_SIZE-1)])) * (int64(enWindow[i+(2<<6)]))) >> 32)
		s_value += int32(((int64(enc.subband.X[ch][(enc.subband.Off[ch]+i+(3<<6))&(HAN_SIZE-1)])) * (int64(enWindow[i+(3<<6)]))) >> 32)
		s_value += int32(((int64(enc.subband.X[ch][(enc.subband.Off[ch]+i+(4<<6))&(HAN_SIZE-1)])) * (int64(enWindow[i+(4<<6)]))) >> 32)
		s_value += int32(((int64(enc.subband.X[ch][(enc.subband.Off[ch]+i+(5<<6))&(HAN_SIZE-1)])) * (int64(enWindow[i+(5<<6)]))) >> 32)
		s_value += int32(((int64(enc.subband.X[ch][(enc.subband.Off[ch]+i+(6<<6))&(HAN_SIZE-1)])) * (int64(enWindow[i+(6<<6)]))) >> 32)
		s_value += int32(((int64(enc.subband.X[ch][(enc.subband.Off[ch]+i+(7<<6))&(HAN_SIZE-1)])) * (int64(enWindow[i+(7<<6)]))) >> 32)
		y[i] = s_value
	}
	enc.subband.Off[ch] = (enc.subband.Off[ch] + 480) & (HAN_SIZE - 1)
	for i = SUBBAND_LIMIT; func() int64 {
		p := &i
		x := *p
		*p--
		return x
	}() != 0; {
		var (
			s_value    int32
			s_value_lo uint32
		)
		_ = s_value_lo
		s_value = 0
		for j = 64; func() int64 {
			p := &j
			x := *p
			*p--
			return x
		}() != 0; {
			s_value += int32(((int64(y[j])) * (int64(enc.subband.Fl[i][j]))) >> 32)
		}
		s[i] = s_value << 1
	}

	return bufPos
}
