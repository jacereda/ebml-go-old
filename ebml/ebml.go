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
// Returs the value read and any error encountered.
func ReadID(r io.Reader) (id uint, err error) {
	var uid uint64
	uid, err, _ = readVint(r)
	id = uint(uid)
	return
}

// ReadUint reads an EBML-encoded size from r.
// Returs the value read and any error encountered.
func ReadSize(r io.Reader) (int64, error) {
	val, err, rem := readVint(r)
	return int64(val & ^(128 << uint(rem*8-rem))), err
}

func readFixed(r io.Reader, sz int) (val uint64, err error) {
	x := make([]uint8, sz)
	_, err = io.ReadFull(r, x)
	for i := 0; i < sz; i++ {
		val <<= 8
		val += uint64(x[i])
	}
	return
}

// ReadUint reads an EBML-encoded uint64 from r.
// Returs the value read and any error encountered.
func ReadUint64(r io.Reader) (val uint64, err error) {
	var sz int64
	sz, err = ReadSize(r)
	if err == nil {
		val, err = readFixed(r, int(sz))
	}
	return
}

// ReadUint reads an EBML-encoded uint from r.
// Returs the value read and any error encountered.
func ReadUint(r io.Reader) (uint, error) {
	val, err := ReadUint64(r)
	return uint(val), err
}

func readSizedString(r io.Reader, sz int64) (string, error) {
	x := make([]byte, sz)
	_, err := io.ReadFull(r, x)
	return string(x), err
}

// ReadString reads an EBML-encoded string from r.
// Returs the value read and any error encountered.
func ReadString(r io.Reader) (s string, err error) {
	var sz int64
	sz, err = ReadSize(r)
	if err == nil {
		s, err = readSizedString(r, sz)
	}
	return
}

// ReadFloat reads an EBML-encoded float64 from r.
// Returs the value read and any error encountered.
func ReadFloat(r io.Reader) (val float64, err error) {
	var sz int64
	var uval uint64
	sz, err = ReadSize(r)
	uval, err = readFixed(r, int(sz))
	if sz == 8 {
		val = math.Float64frombits(uval)
	} else {
		val = float64(math.Float32frombits(uint32(uval)))
	}
	return
}

// Skip skips the next element in r.
func Skip(r io.Reader) (err error) {
	_, err = ReadString(r)
	return
}

// Locate skips elements until it finds the required ElementID.
// Returs the size of the element and any error found.
func Locate(r io.Reader, reqid uint) (sz int64, err error) {
	var id uint
	for id != reqid && err == nil {
		id, err = ReadID(r)
		if err == nil {
			sz, err = ReadSize(r)
		}
	}
	if id != reqid {
		err = errors.New("Unable to find ID " + string(reqid))
	}
	return
}

// Read reads EBML data from r into data. Data must be a pointer to a
// struct. Fields present in the struct but absent in the stream will
// just keep their zero value.
func Read(r io.Reader, val interface{}) error {
	return readStruct(r, reflect.Indirect(reflect.ValueOf(val)))
}

func getTag(f reflect.StructField, s string) uint {
	sid := f.Tag.Get(s)
	id, _ := strconv.ParseUint(sid, 16, 0)
	return uint(id)
}

func lookup(reqid uint, t reflect.Type) int {
	for i, l := 0, t.NumField(); i < l; i++ {
		if getTag(t.Field(i), "id") == reqid {
			return i
		}
	}
	return -1
}

func sectionReader(r io.Reader) (sr io.Reader, err error) {
	var sz int64
	sz, err = ReadSize(r)
	if err == nil {
		sr = io.LimitReader(r, sz)
	}
	return
}

func readStruct(r io.Reader, v reflect.Value) (err error) {
	for err == nil {
		var id uint
		id, err = ReadID(r)
		if err == io.EOF {
			err = nil
			break
		}
		i := lookup(id, v.Type())
		if i >= 0 {
			err = readField(r, v.Field(i))
		} else {
			err = Skip(r)
		}
	}
	return
}

func readField(r io.Reader, v reflect.Value) (err error) {
	var lr io.Reader
	switch v.Kind() {
	case reflect.Struct:
		lr, err = sectionReader(r)
		if err == nil {
			err = readStruct(lr, v)
		}
	case reflect.Slice:
		lr, err = sectionReader(r)
		if err == nil {
			err = readSlice(lr.(*io.LimitedReader), v)
		}
	case reflect.Array:
		lr, err = sectionReader(r)
		for i, l := 0, v.Len(); i < l && err == nil; i++ {
			err = readStruct(lr, v.Index(i))
		}
	case reflect.String:
		var s string
		s, err = ReadString(r)
		v.SetString(s)
	case reflect.Int:
		var u uint64
		u, err = ReadUint64(r)
		v.SetInt(int64(u))
	case reflect.Uint:
		var u uint64
		u, err = ReadUint64(r)
		v.SetUint(u)
	case reflect.Float32:
		fallthrough
	case reflect.Float64:
		var f float64
		f, err = ReadFloat(r)
		v.SetFloat(f)
	default:
		err = errors.New("Unknown type: " + v.String())
	}
	return
}

func readSlice(lr *io.LimitedReader, v reflect.Value) (err error) {
	switch v.Type().Elem().Kind() {
	case reflect.Uint8:
		sl := make([]uint8, lr.N, lr.N)
		io.ReadFull(lr, sl)
		v.Set(reflect.ValueOf(sl))
	case reflect.Struct:
		vl := v.Len()
		ne := reflect.New(v.Type().Elem())
		nsl := reflect.Append(v, reflect.Indirect(ne))
		v.Set(nsl)
		if err == nil {
			err = readStruct(lr, v.Index(vl))
		}
	default:
		err = errors.New("Unknown slice type: " + v.String())
	}
	return
}
