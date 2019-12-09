/*

Writer implementation.

*/

package bitio

import (
	"bufio"
	"io"
)

// An io.Writer and io.ByteWriter at the same time.
type writerAndByteWriter interface {
	io.Writer
	io.ByteWriter
}

// Writer is the bit writer implementation.
//
// For convenience, it also implements io.WriterCloser and io.ByteWriter.
type Writer struct {
	out       writerAndByteWriter
	wrapperbw *bufio.Writer // wrapper bufio.Writer if the target does not implement io.ByteWriter
	cache     byte          // unwritten bits are stored here
	bits      byte          // number of unwritten bits in cache

	// TryError holds the first error occurred in TryXXX() methods.
	TryError error
}

// NewWriter returns a new Writer using the specified io.Writer as the output.
//
// Must be closed in order to flush cached data.
// If you can't or don't want to close it, flushing data can also be forced
// by calling Align().
func NewWriter(out io.Writer) *Writer {
	w := &Writer{}
	var ok bool
	w.out, ok = out.(writerAndByteWriter)
	if !ok {
		w.wrapperbw = bufio.NewWriter(out)
		w.out = w.wrapperbw
	}
	return w
}

// Write writes len(p) bytes (8 * len(p) bits) to the underlying writer.
//
// Write implements io.Writer, and gives a byte-level interface to the bit stream.
// This will give best performance if the underlying io.Writer is aligned
// to a byte boundary (else all the individual bytes are spread to multiple bytes).
// Byte boundary can be ensured by calling Align().
func (w *Writer) Write(p []byte) (n int, err error) {
	// w.bits will be the same after writing 8 bits, so we don't need to update that.
	if w.bits == 0 {
		return w.out.Write(p)
	}

	for i, b := range p {
		if err = w.writeUnalignedByte(b); err != nil {
			return i, err
		}
	}

	return len(p), nil
}

// WriteBits writes out the n lowest bits of r.
// Bits of r in positions higher than n are ignored.
func (w *Writer) WriteBits(r uint64, n uint8) (err error) {
	// if r would have bits set higher than n-1 (zero indexed),
	// the below implementation could "corrupt" bits in cache.
	// That is not acceptable. To be on the safe side, mask out higher bits:
	r &= 1<<n - 1

	// Some optimization, frequent cases
	newbits := w.bits + n
	if newbits < 8 {
		// r fits into cache, no write will occur to out
		w.cache |= byte(r) << (8 - newbits)
		w.bits = newbits
		return nil
	}

	if newbits > 8 {
		// cache will be filled, and there will be more bits to write
		// "Fill cache" and write it out
		free := 8 - w.bits
		err = w.out.WriteByte(w.cache | byte(r>>(n-free)))
		if err != nil {
			return
		}
		n -= free
		// write out whole bytes
		for n >= 8 {
			n -= 8
			// No need to mask r, converting to byte will mask out higher bits
			err = w.out.WriteByte(byte(r >> n))
			if err != nil {
				return
			}
		}
		// Put remaining into cache
		if n > 0 {
			// Note: n < 8 (in case of n=8, 1<<n would overflow byte)
			w.cache, w.bits = (byte(r)&((1<<n)-1))<<(8-n), n
		} else {
			w.cache, w.bits = 0, 0
		}
		return nil
	}

	// cache will be filled exactly with the bits to be written
	bb := w.cache | byte(r)
	w.cache, w.bits = 0, 0
	return w.out.WriteByte(bb)
}

// WriteByte writes 8 bits.
//
// WriteByte implements io.ByteWriter.
func (w *Writer) WriteByte(b byte) (err error) {
	// w.bits will be the same after writing 8 bits, so we don't need to update that.
	if w.bits == 0 {
		return w.out.WriteByte(b)
	}
	return w.writeUnalignedByte(b)
}

// writeUnalignedByte writes 8 bits which are (may be) unaligned.
func (w *Writer) writeUnalignedByte(b byte) (err error) {
	// w.bits will be the same after writing 8 bits, so we don't need to update that.
	bits := w.bits
	err = w.out.WriteByte(w.cache | b>>bits)
	if err != nil {
		return
	}
	w.cache = (b & (1<<bits - 1)) << (8 - bits)
	return
}

// WriteBool writes one bit: 1 if param is true, 0 otherwise.
func (w *Writer) WriteBool(b bool) (err error) {
	if w.bits == 7 {
		if b {
			err = w.out.WriteByte(w.cache | 1)
		} else {
			err = w.out.WriteByte(w.cache)
		}
		if err != nil {
			return
		}
		w.cache, w.bits = 0, 0
		return nil
	}

	w.bits++
	if b {
		w.cache |= 1 << (8 - w.bits)
	}
	return nil
}

// Align aligns the bit stream to a byte boundary,
// so next write will start/go into a new byte.
// If there are cached bits, they are first written to the output.
// Returns the number of skipped (unset but still written) bits.
func (w *Writer) Align() (skipped uint8, err error) {
	if w.bits > 0 {
		if err = w.out.WriteByte(w.cache); err != nil {
			return
		}

		skipped = 8 - w.bits
		w.cache, w.bits = 0, 0
	}
	if w.wrapperbw != nil {
		err = w.wrapperbw.Flush()
	}
	return
}

// TryWrite tries to write len(p) bytes (8 * len(p) bits) to the underlying writer.
//
// If there was a previous TryError, it does nothing. Else it calls Write(),
// returns the data it provides and stores the error in the TryError field.
func (w *Writer) TryWrite(p []byte) (n int) {
	if w.TryError == nil {
		n, w.TryError = w.Write(p)
	}
	return
}

// TryWriteBits tries to write out the n lowest bits of r.
//
// If there was a previous TryError, it does nothing. Else it calls WriteBits(),
// and stores the error in the TryError field.
func (w *Writer) TryWriteBits(r uint64, n uint8) {
	if w.TryError == nil {
		w.TryError = w.WriteBits(r, n)
	}
}

// TryWriteByte tries to write 8 bits.
//
// If there was a previous TryError, it does nothing. Else it calls WriteByte(),
// and stores the error in the TryError field.
func (w *Writer) TryWriteByte(b byte) {
	if w.TryError == nil {
		w.TryError = w.WriteByte(b)
	}
}

// TryWriteBool tries to write one bit: 1 if param is true, 0 otherwise.
//
// If there was a previous TryError, it does nothing. Else it calls WriteBool(),
// and stores the error in the TryError field.
func (w *Writer) TryWriteBool(b bool) {
	if w.TryError == nil {
		w.TryError = w.WriteBool(b)
	}
}

// TryAlign tries to align the bit stream to a byte boundary.
//
// If there was a previous TryError, it does nothing. Else it calls Align(),
// returns the data it provides and stores the error in the TryError field.
func (w *Writer) TryAlign() (skipped uint8) {
	if w.TryError == nil {
		skipped, w.TryError = w.Align()
	}
	return
}

// Close closes the bit writer, writes out cached bits.
// It does not close the underlying io.Writer.
//
// Close implements io.Closer.
func (w *Writer) Close() (err error) {
	// Make sure cached bits are flushed:
	if _, err = w.Align(); err != nil {
		return
	}

	return nil
}
