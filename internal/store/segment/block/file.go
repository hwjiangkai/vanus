// Copyright 2022 Linkall Inc.
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

package block

import (
	"bytes"
	"context"
	"encoding/binary"
	"github.com/linkall-labs/vanus/internal/store/segment/codec"
	"github.com/linkall-labs/vanus/observability"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

const (
	fileSegmentBlockHeaderCapacity = 1024

	// version + capacity + length + number
	v1FileSegmentBlockHeaderLength = 4 + 8 + 4 + 8
	v1IndexLength                  = 12
)

type fileBlock struct {
	version                       int32
	id                            string
	path                          string
	capacity                      int64
	length                        int64
	number                        int32
	writeOffset                   int64
	readOffset                    int64
	appendMutex                   sync.Mutex
	physicalFile                  *os.File
	indexes                       []blockIndex
	readable                      atomic.Value
	appendable                    atomic.Value
	fullFlag                      atomic.Value
	uncompletedReadRequestCount   int32
	uncompletedAppendRequestCount int32
}

func (b *fileBlock) Initialize(ctx context.Context) error {
	if err := b.loadHeader(ctx); err != nil {
		return err
	}

	if err := b.loadIndex(ctx); err != nil {
		return err
	}

	if err := b.validate(ctx); err != nil {
		return err
	}
	return nil
}

func (b *fileBlock) Append(ctx context.Context, entities ...*codec.StoredEntry) error {
	observability.EntryMark(ctx)
	b.appendMutex.Lock()
	atomic.AddInt32(&(b.uncompletedAppendRequestCount), 1)
	defer func() {
		observability.LeaveMark(ctx)
		b.appendMutex.Unlock()
		atomic.AddInt32(&(b.uncompletedAppendRequestCount), -1)
	}()

	if len(entities) == 0 {
		return nil
	}
	buf := bytes.NewBuffer(make([]byte, 0))
	length := 0
	idxes := make([]blockIndex, 0)
	for idx := range entities {
		data, err := codec.Marshall(entities[idx])
		if err != nil {
			return err
		}
		bi := blockIndex{
			startOffset: b.writeOffset + int64(length),
		}
		if _, err = buf.Write(data); err != nil {
			return err
		}
		length += len(data)
		bi.length = int32(len(data))
		idxes = append(idxes, bi)
	}
	// TODO optimize this
	// if the file has been left many space, but received a large request, the remain space will be wasted
	if length > b.remain(int64(length+v1IndexLength*len(idxes))) {
		return ErrNoEnoughCapacity
	}
	n, err := b.physicalFile.Write(buf.Bytes())
	b.indexes = append(b.indexes, idxes...)
	atomic.AddInt32(&b.number, int32(len(idxes)))
	atomic.AddInt64(&b.writeOffset, int64(n))
	return err
}

// Read date from file
func (b *fileBlock) Read(ctx context.Context, entityStartOffset, number int) ([]*codec.StoredEntry, error) {
	observability.EntryMark(ctx)
	atomic.AddInt32(&(b.uncompletedReadRequestCount), 1)
	defer func() {
		observability.LeaveMark(ctx)
		atomic.AddInt32(&(b.uncompletedReadRequestCount), -1)
	}()

	from, to, err := b.calculateRange(entityStartOffset, number)
	if err != nil {
		return nil, err
	}

	data := make([]byte, to-from)
	if _, err := b.physicalFile.ReadAt(data, from); err != nil {
		return nil, err
	}

	ses := make([]*codec.StoredEntry, 0)
	reader := bytes.NewReader(data)
	for err == nil {
		size := int32(0)
		if err = binary.Read(reader, binary.BigEndian, &size); err != nil {
			break
		}
		payload := make([]byte, int(size))
		if _, err = reader.Read(payload); err != nil {
			break
		}
		se := &codec.StoredEntry{
			Length:  size,
			Payload: payload,
		}
		ses = append(ses, se)
	}
	if err != io.EOF {
		return nil, err
	}

	return ses, nil
}

func (b *fileBlock) CloseWrite(ctx context.Context) error {
	observability.EntryMark(ctx)
	defer observability.LeaveMark(ctx)

	b.appendable.Store(false)
	for b.uncompletedAppendRequestCount != 0 {
		time.Sleep(time.Millisecond)
	}

	if err := b.persistHeader(ctx); err != nil {
		return err
	}

	if err := b.persistIndex(ctx); err != nil {
		return err
	}
	return nil
}

func (b *fileBlock) CloseRead(ctx context.Context) error {
	if err := b.physicalFile.Close(); err != nil {
		return err
	}
	observability.EntryMark(ctx)
	defer observability.LeaveMark(ctx)

	b.readable.Store(false)
	for b.uncompletedReadRequestCount != 0 {
		time.Sleep(time.Millisecond)
	}
	return nil
}

func (b *fileBlock) Close(ctx context.Context) error {
	observability.EntryMark(ctx)
	defer observability.LeaveMark(ctx)
	return b.physicalFile.Close()
}

func (b *fileBlock) IsAppendable() bool {
	return b.appendable.Load().(bool) && !b.IsFull()
}

func (b *fileBlock) IsReadable() bool {
	return b.appendable.Load().(bool) && !b.IsEmpty()
}

func (b *fileBlock) IsEmpty() bool {
	return b.length == fileSegmentBlockHeaderCapacity
}

func (b *fileBlock) IsFull() bool {
	return b.fullFlag.Load().(bool)
}

func (b *fileBlock) Path() string {
	return b.path
}

func (b *fileBlock) SegmentBlockID() string {
	return b.id
}

func (b *fileBlock) remain(sizeNeedServed int64) int {
	return int(b.capacity-b.length-int64(b.number*v1IndexLength)-sizeNeedServed) - fileSegmentBlockHeaderCapacity
}

func (b *fileBlock) persistHeader(ctx context.Context) error {
	b.appendMutex.Lock()
	defer b.appendMutex.Unlock()
	buf := bytes.NewBuffer(make([]byte, 0))

	if err := binary.Write(buf, binary.BigEndian, b.version); err != nil {
		return err
	}
	if err := binary.Write(buf, binary.BigEndian, b.capacity); err != nil {
		return err
	}
	if err := binary.Write(buf, binary.BigEndian, b.length); err != nil {
		return err
	}
	if err := binary.Write(buf, binary.BigEndian, b.number); err != nil {
		return err
	}

	// TODO does it safe when concurrent write and append?
	if _, err := b.physicalFile.WriteAt(buf.Bytes(), 0); err != nil {
		return err
	}
	return nil
}

func (b *fileBlock) loadHeader(ctx context.Context) error {
	hd := make([]byte, v1FileSegmentBlockHeaderLength)
	if _, err := b.physicalFile.ReadAt(hd, 0); err != nil {
		return err
	}
	reader := bytes.NewReader(hd)
	if err := binary.Read(reader, binary.BigEndian, &b.version); err != nil {
		return err
	}
	if err := binary.Read(reader, binary.BigEndian, &b.capacity); err != nil {
		return err
	}
	if err := binary.Read(reader, binary.BigEndian, &b.length); err != nil {
		return err
	}
	if err := binary.Read(reader, binary.BigEndian, &b.number); err != nil {
		return err
	}
	b.writeOffset = v1FileSegmentBlockHeaderLength + b.length
	return nil
}

func (b *fileBlock) persistIndex(ctx context.Context) error {
	if !b.IsFull() {
		return nil
	}
	buf := bytes.NewBuffer(make([]byte, 0))
	for idx := range b.indexes {
		if err := binary.Write(buf, binary.BigEndian, b.indexes[idx].startOffset); err != nil {
			return err
		}
		if err := binary.Write(buf, binary.BigEndian, b.indexes[idx].length); err != nil {
			return err
		}
	}
	if _, err := b.physicalFile.WriteAt(buf.Bytes(), b.writeOffset); err != nil {
		return err
	}
	return nil
}

func (b *fileBlock) loadIndex(ctx context.Context) error {
	b.indexes = make([]blockIndex, b.number)
	if b.IsFull() {
		// read index directly
		idxData := make([]byte, b.number*v1IndexLength)
		if _, err := b.physicalFile.ReadAt(idxData, b.writeOffset); err != nil {
			return err
		}
		reader := bytes.NewReader(idxData)
		for idx := range b.indexes {
			if err := binary.Read(reader, binary.BigEndian, &b.indexes[idx].startOffset); err != nil {
				return err
			}
			if err := binary.Read(reader, binary.BigEndian, &b.indexes[idx].length); err != nil {
				return err
			}
		}
	} else {
		// rebuild index
		off := int64(fileSegmentBlockHeaderCapacity)
		ld := make([]byte, 4)
		for idx := 0; idx < int(b.number); idx++ {
			if _, err := b.physicalFile.ReadAt(ld, off); err != nil {
				return err
			}
			reader := bytes.NewReader(ld)
			var entityLen int32
			if err := binary.Read(reader, binary.BigEndian, &entityLen); err != nil {
				return err
			}
			b.indexes[idx].startOffset = off
			b.indexes[idx].length = entityLen
			off += 4 + int64(entityLen)
		}
	}
	return nil
}

func (b *fileBlock) validate(ctx context.Context) error {
	return nil
}

func (b *fileBlock) calculateRange(start, num int) (int64, int64, error) {
	if start >= len(b.indexes) {
		return -1, -1, ErrOffsetExceeded
	}
	startPos := b.indexes[start].startOffset
	var endPos int64
	offset := start + num - 1
	if b.number < int32(offset+1) {
		endPos = b.indexes[b.number-1].startOffset + int64(b.indexes[b.number-1].length)
	} else {
		endPos = b.indexes[offset].startOffset + int64(b.indexes[offset].length)
	}
	return startPos, endPos, nil
}

type blockIndex struct {
	startOffset int64
	length      int32
}
