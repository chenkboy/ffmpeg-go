package buffer

import (
	"errors"
	"io"
)

type Buffer struct {
	data   []byte
	offset int64
}

func NewEmptyBuffer() *Buffer {
	return &Buffer{
		data:   make([]byte, 0),
		offset: 0,
	}
}

func NewBuffer(b []byte) *Buffer {
	return &Buffer{
		data:   b,
		offset: 0,
	}
}

func (b *Buffer) Write(p []byte) (n int, err error) {
	bufSize := len(p)
	if int(b.offset) < len(b.data) {
		b.data = append(append(b.data[:b.offset], p...), b.data[int(b.offset)+bufSize:]...)
	} else {
		b.data = append(b.data, p...)
	}
	n = bufSize
	b.offset += int64(n)
	return
}

func (b *Buffer) Read(p []byte) (n int, err error) {
	bufSize := len(p)
	if int(b.offset) > len(b.data) {
		return
	}
	if int(b.offset)+bufSize < len(b.data) {
		copy(p, b.data[b.offset:int(b.offset)+bufSize])
	} else {
		copy(p, b.data[b.offset:])
	}
	n = bufSize
	b.offset += int64(n)
	return
}

func (b *Buffer) Bytes() []byte {
	return b.data
}

func (b *Buffer) Seek(offset int64, whence int) (int64, error) {
	var newOffset int64
	switch whence {
	case io.SeekStart:
		newOffset = offset
	case io.SeekCurrent:
		newOffset = b.offset + offset
	case io.SeekEnd:
		newOffset = int64(len(b.data)) + offset
	default:
		return 0, errors.New("invalid whence value")
	}

	if newOffset < 0 {
		return 0, errors.New("negative offset")
	}

	b.offset = newOffset
	return b.offset, nil
}

func (b *Buffer) Clear() {
	b.data = make([]byte, 0)
	b.offset = 0
}
