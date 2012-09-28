// Copyright (c) 2012, Jorge Acereda Maciá. All rights reserved.  
//
// Use of this source code is governed by a BSD-style license that can
// be found in the LICENSE file.

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
	"fmt"
	"io"
	"log"
	"math"
	"reflect"
	"strconv"
)

// ReachedPayloadError is generated when a field tagged with
// ebmlstop:"1" is reached.
type ReachedPayloadError struct {
	First *Element
	Rest  *Element
}

func (r ReachedPayloadError) Error() string {
	return "Reached payload"
}

// Element represents an EBML-encoded chunk of data.
type Element struct {
	R  io.Reader
	Id uint
}

func (e *Element) String() string {
	return fmt.Sprintf("{%+v %x}", e.R, e.Id)
}

// Size returns the size of the element.
func (e *Element) Size() int64 {
	lr := e.R.(*io.LimitedReader)
	return lr.N
}

// Creates the root element corresponding to the data available in r.
func RootElement(r io.Reader) (*Element, error) {
	e := &Element{io.LimitReader(r, math.MaxInt64), 0}
	return e, nil
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

func readSize(r io.Reader) (int64, error) {
	val, err, rem := readVint(r)
	return int64(val & ^(128 << uint(rem*8-rem))), err
}

// Next returns the next child element in an element.
func (e *Element) Next() (*Element, error) {
	var ne Element
	id, err, _ := readVint(e.R)
	if err != nil {
		return nil, err
	}
	var sz int64
	sz, err = readSize(e.R)
	if err != nil {
		return nil, err
	}
	ne.R = io.LimitReader(e.R, sz)
	ne.Id = uint(id)
	return &ne, err
}

func (e *Element) readUint64() (uint64, error) {
	d, err := e.ReadData()
	var i int
	sz := len(d)
	var val uint64
	for i = 0; i < sz; i++ {
		val <<= 8
		val += uint64(d[i])
	}
	return val, err
}

func (e *Element) readUint() (uint, error) {
	val, err := e.readUint64()
	return uint(val), err
}

func (e *Element) readString() (string, error) {
	s, err := e.ReadData()
	return string(s), err
}

func (e *Element) ReadData() (d []byte, err error) {
	sz := e.Size()
	d = make([]uint8, sz, sz)
	_, err = io.ReadFull(e.R, d)
	return
}

func (e *Element) readFloat() (val float64, err error) {
	var uval uint64
	sz := e.Size()
	uval, err = e.readUint64()
	if sz == 8 {
		val = math.Float64frombits(uval)
	} else {
		val = float64(math.Float32frombits(uint32(uval)))
	}
	return
}

func (e *Element) skip() (err error) {
	_, err = e.ReadData()
	return
}

// Unmarshal reads EBML data from r into data. Data must be a pointer
// to a struct. Fields present in the struct but absent in the stream
// will just keep their zero value.
// Returns an error that can be an io.Error or a ReachedPayloadError
// containing the first element and the the parent element containing
// the rest of the elements.
func (e *Element) Unmarshal(val interface{}) error {
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
			return i - 1000000*int(getTag(f, "ebmlstop"))
		}
	}
	return -1
}

func setDefaults(v reflect.Value) {
	t := v.Type()
	for i, l := 0, t.NumField(); i < l; i++ {
		fv := v.Field(i)
		switch fv.Kind() {
		case reflect.Int, reflect.Uint, 
			reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, 
			reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, 
			reflect.Float32, reflect.Float64, 
			reflect.String:
			setFieldDefaults(fv, t.Field(i), v)
		case reflect.Array, reflect.Struct, reflect.Slice:
			break
		default:
			log.Panic("Unsupported type")
		}
	}
}

func setFieldDefaults(v reflect.Value, sf reflect.StructField, s reflect.Value) {
	if v.CanInterface() && reflect.DeepEqual(
		v.Interface(), reflect.Zero(v.Type()).Interface()) {
		tag := sf.Tag.Get("ebmldef")
		if tag != "" {
			switch v.Kind() {
			case reflect.Int, reflect.Int64:
				u, _ := strconv.ParseInt(tag, 10, 0)
				v.SetInt(int64(u))
			case reflect.Uint, reflect.Uint64:
				u, _ := strconv.ParseUint(tag, 10, 0)
				v.SetUint(u)
			case reflect.Float32, reflect.Float64:
				f, _ := strconv.ParseFloat(tag, 64)
				v.SetFloat(f)
			case reflect.String:
				v.SetString(tag)
			default:
				log.Panic("Unsupported default value")
			}
		}
		ltag := sf.Tag.Get("ebmldeflink")
		if ltag != "" {
			v.Set(s.FieldByName(ltag))
		}
	}
}

func (e *Element) readStruct(v reflect.Value) (err error) {
	t := v.Type()
	for err == nil {
		var ne *Element
		ne, err = e.Next()
		if err == io.EOF {
			err = nil
			break
		}
		i := lookup(ne.Id, t)
		if i >= 0 {
			err = ne.readField(v.Field(i))
		} else if i == -1 {
			err = ne.skip()
		} else {
			err = ReachedPayloadError{ne, e}
		}
	}
	setDefaults(v)
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
		s, err = e.readString()
		v.SetString(s)
	case reflect.Int, reflect.Int64:
		var u uint64
		u, err = e.readUint64()
		v.SetInt(int64(u))
	case reflect.Uint, reflect.Uint64:
		var u uint64
		u, err = e.readUint64()
		v.SetUint(u)
	case reflect.Float32, reflect.Float64:
		var f float64
		f, err = e.readFloat()
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
