// Copyright 2011 The ebml-go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.


// Package ebml decodes EBML data.
//
// EBML is short for Extensible Binary Meta Language. EBML specifies a
// binary and octet (byte) aligned format inspired by the principle of
// XML. EBML itself is a generalized description of the technique of
// binary markup. Like XML, it is completely agnostic to any data that it
// might contain. 
// For a specification, see http://ebml.sourceforge.net/specs/
package ebml

import (
	"errors"
	"io"
	"math"
	"reflect"
	"strconv"
)


type ReachedPayloadError struct {
	P *Element
	E *Element
}
 
func (r ReachedPayloadError) Error() string {
	return "Reached payload"
}

type Element struct {
	R io.Reader
	Id uint
}

func (e *Element) Size() int64 {
	lr := e.R.(*io.LimitedReader)
	return lr.N
}

func RootElement(r io.Reader) (*Element, error) {
	e := &Element{io.LimitReader(r, math.MaxInt64), 0}
	return e,nil
}

func remaining(x int8) (rem int) {
	for x > 0 {
		rem++
		x += x
	}
	return
}

func readVint(r io.Reader) (val uint64, err error, rem int) {
	v := make([]uint8, 1)
	_, err = io.ReadFull(r, v)
	if err == nil {
		val = uint64(v[0])
		rem = remaining(int8(val))
		for i := 0; err == nil && i < rem; i++ {
			_, err = io.ReadFull(r, v)
			val <<= 8
			val += uint64(v[0])
		}
	}
	return
}

// ReadUint reads an EBML-encoded ElementID from r.
func readID(r io.Reader) (id uint, err error) {
	var uid uint64
	uid, err, _ = readVint(r)
	id = uint(uid)
	return
}

func readSize(r io.Reader) (int64, error) {
	val, err, rem := readVint(r)
	return int64(val & ^(128 << uint(rem*8-rem))), err
}

func (e *Element) Next() (*Element, error) {
	var ne Element
	id,err := readID(e.R)
	if err != nil {
		return nil,err
	}
	var sz int64
	sz,err = readSize(e.R)
	if err != nil {
		return nil,err
	}
	ne.R = io.LimitReader(e.R, sz)
	ne.Id = id
	return &ne,err
}

func readFixed(r io.Reader, sz int64) (val uint64, err error) {
	x := make([]uint8, sz)
	_, err = io.ReadFull(r, x)
	var i int64
	for i = 0; i < sz; i++ {
		val <<= 8
		val += uint64(x[i])
	}
	return
}

// ReadUint reads an EBML-encoded uint64 from r.
func (e *Element) ReadUint64() (uint64, error) {
	return readFixed(e.R, e.Size())
}

// ReadUint reads an EBML-encoded uint from r.
func (e *Element) ReadUint() (uint, error) {
	val, err := e.ReadUint64()
	return uint(val), err
}

func readSizedString(r io.Reader, sz int64) (string, error) {
	x := make([]byte, sz)
	_, err := io.ReadFull(r, x)
	return string(x), err
}

// ReadString reads an EBML-encoded string from r.
func (e *Element) ReadString() (string, error) {
	s,err := e.ReadData()
	return string(s), err
}

func (e *Element) ReadData() (d []byte, err error) {
	sz := e.Size()
	d = make([]uint8, sz, sz)
	_,err = io.ReadFull(e.R, d)
	return
}

// ReadFloat reads an EBML-encoded float64 from r.
func (e *Element) ReadFloat() (val float64, err error) {
	var uval uint64
	uval, err = e.ReadUint64()
	if e.Size() == 8 {
		val = math.Float64frombits(uval)
	} else {
		val = float64(math.Float32frombits(uint32(uval)))
	}
	return
}

// Skip skips the next element in r.
func (e *Element) Skip() (err error) {
	_, err = e.ReadData()
	return
}

// Locate skips elements until it finds the required ElementID.
func (e *Element) Locate(reqid uint) (err error) {
	var ne *Element
	for {
		ne, err = e.Next()
		if ne.Id == reqid {
			return
		}
		err = ne.Skip()
		if err != nil {
			return
		}
	}
	return
}

// Read reads EBML data from r into data. Data must be a pointer to a
// struct. Fields present in the struct but absent in the stream will
// just keep their zero value.
func (e *Element)Unmarshal(val interface{}) (error) {
	return e.readStruct(reflect.Indirect(reflect.ValueOf(val)))
}

func getTag(f reflect.StructField, s string) uint {
	sid := f.Tag.Get(s)
	id, _ := strconv.ParseUint(sid, 16, 0)
	return uint(id)
}

func lookup(reqid uint, t reflect.Type) int {
	for i, l := 0, t.NumField(); i < l; i++ {
		f := t.Field(i)
		if getTag(f, "ebml") == reqid {
			return i - 1000000 * int(getTag(f, "ebmlstop"))
		}
	}
	return -1
}

func (e *Element) readStruct(v reflect.Value) (err error) {
	for err == nil {
		var ne *Element
		ne,err = e.Next()
		if err == io.EOF {
			err = nil;
			break;
		}
		i := lookup(ne.Id, v.Type())
		if (i >= 0) {
			err = ne.readField(v.Field(i))
		} else if i == -1 {
			err = ne.Skip()
		} else {
			err = ReachedPayloadError{e, ne}
		}
	}
	return
}

func (e *Element) readField(v reflect.Value) (err error) {
	switch v.Kind() {
	case reflect.Struct:
		err = e.readStruct(v)
	case reflect.Slice:
		err = e.readSlice(v)
	case reflect.Array:
		for i, l := 0, v.Len(); i < l && err == nil; i++ {
			err = e.readStruct(v.Index(i))
		}
	case reflect.String:
		var s string
		s, err = e.ReadString()
		v.SetString(s)
	case reflect.Int:
		fallthrough
	case reflect.Int64:
		var u uint64
		u, err = e.ReadUint64()
		v.SetInt(int64(u))
	case reflect.Uint:
		fallthrough
	case reflect.Uint64:
		var u uint64
		u, err = e.ReadUint64()
		v.SetUint(u)
	case reflect.Float32:
		fallthrough
	case reflect.Float64:
		var f float64
		f, err = e.ReadFloat()
		v.SetFloat(f)
	default:
		err = errors.New("Unknown type: " + v.String())
	}
	return
}

func (e *Element) readSlice(v reflect.Value) (err error) {
	switch v.Type().Elem().Kind() {
	case reflect.Uint8:
		var sl []uint8
		sl, err = e.ReadData()
		if err == nil {
			v.Set(reflect.ValueOf(sl))
		}
	case reflect.Struct:
		vl := v.Len()
		ne := reflect.New(v.Type().Elem())
		nsl := reflect.Append(v, reflect.Indirect(ne))
		v.Set(nsl)
		err = e.readStruct(v.Index(vl))
	default:
		err = errors.New("Unknown slice type: " + v.String())
	}
	return
}
