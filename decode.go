// Copyright 2013 Alvaro J. Genial. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package form

import (
	"fmt"
	"io"
	"io/ioutil"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// NewDecoder returns a new form decoder.
func NewDecoder(r io.Reader) *decoder {
	return &decoder{r}
}

// decoder decodes data from a form (application/x-www-form-urlencoded).
type decoder struct {
	r io.Reader
}

// Decode reads in and decodes form-encoded data into dst.
func (d decoder) Decode(dst interface{}) error {
	bs, err := ioutil.ReadAll(d.r)
	if err != nil {
		return err
	}
	vs, err := url.ParseQuery(string(bs))
	if err != nil {
		return err
	}
	v := reflect.ValueOf(dst)
	return decodeNode(v, parseValues(vs, canIndex(v)))
}

// DecodeString decodes src into dst.
func DecodeString(dst interface{}, src string) error {
	vs, err := url.ParseQuery(src)
	if err != nil {
		return err
	}
	v := reflect.ValueOf(dst)
	return decodeNode(v, parseValues(vs, canIndex(v)))
}

// DecodeValues decodes vs into dst.
func DecodeValues(dst interface{}, vs url.Values) error {
	v := reflect.ValueOf(dst)
	return decodeNode(v, parseValues(vs, canIndex(v)))
}

func decodeNode(v reflect.Value, n node) (err error) {
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("%v", e)
		}
	}()

	if v.Kind() == reflect.Slice {
		return fmt.Errorf("could not decode directly into slice; use pointer to slice")
	}
	decodeValue(v, n)
	return nil
}

func decodeValue(v reflect.Value, x interface{}) {
	t := v.Type()
	k := v.Kind()

	switch k {
	case reflect.Ptr, reflect.Interface:
		decodeValue(v.Elem(), x)
		return
	}

	if s, ok := x.(string); ok && s == "" { // Treat the empty string as the zero value.
		v.Set(reflect.Zero(t))
		return
	}

	switch k {
	case reflect.Struct:
		if t.ConvertibleTo(timeType) {
			decodeTime(v, x)
		} else {
			decodeStruct(v, x)
		}
	case reflect.Slice:
		decodeSlice(v, x)
	case reflect.Array:
		decodeArray(v, x)
	case reflect.Map:
		decodeMap(v, x)
	case reflect.Invalid, reflect.Uintptr, reflect.UnsafePointer,
		reflect.Complex64, reflect.Complex128, reflect.Chan, reflect.Func:
		panic(t.String() + " has unsupported kind " + t.Kind().String())
	default:
		decodeBasic(v, x)
	}
}

func fieldInfo(f reflect.StructField) (k string, oe bool) {
	if f.PkgPath != "" { // Skip private fields.
		return "-", oe
	}

	k = f.Name
	tag := f.Tag.Get("form")
	if tag == "" {
		return k, oe
	}

	ps := strings.SplitN(tag, ",", 2)
	if ps[0] != "" {
		k = ps[0]
	}
	if len(ps) == 2 {
		oe = ps[1] == "omitempty"
	}
	return k, oe
}

func findField(v reflect.Value, n string) (reflect.Value, bool) {
	t := v.Type()
	for i, l := 0, v.NumField(); i < l; i++ {
		f := t.Field(i)
		k, _ := fieldInfo(f)
		if k == "-" {
			continue
		} else if n == k {
			return v.Field(i), true
		}
	}
	return reflect.Value{}, false
}

func decodeStruct(v reflect.Value, x interface{}) {
	t := v.Type()
	for k, c := range getNode(x) {
		if f, ok := findField(v, k); !ok {
			panic(k + " doesn't exist in " + t.String())
		} else if !f.CanSet() {
			panic(k + " cannot be set in " + t.String())
		} else {
			decodeValue(f, c)
		}
	}
}

func decodeMap(v reflect.Value, x interface{}) {
	t := v.Type()
	if v.IsNil() {
		v.Set(reflect.MakeMap(t))
	}
	for k, c := range getNode(x) {
		i := reflect.New(t.Key()).Elem()
		decodeBasic(i, k) // TODO: decodeValue.

		w := v.MapIndex(i)
		if w.IsValid() { // We have an actual element value to decode into.
			if w.Kind() == reflect.Interface {
				w = w.Elem()
			}
			w = reflect.New(w.Type()).Elem()
		} else if t.Elem().Kind() != reflect.Interface { // The map's element type is concrete.
			w = reflect.New(t.Elem()).Elem()
		} else {
			// The best we can do here is to decode as either a string (for scalars) or a map[string]interface {} (for the rest).
			// We could try to guess the type based on the string (e.g. true/false => bool) but that'll get ugly fast,
			// especially if we have to guess the kind (slice vs. array vs. map) and index type (e.g. string, int, etc.)
			switch c.(type) {
			case node:
				w = reflect.MakeMap(stringMapType)
			case string:
				w = reflect.New(stringType).Elem()
			default:
				panic("value is neither node nor string")
			}
		}

		decodeValue(w, c)
		v.SetMapIndex(i, w)
	}
}

func decodeArray(v reflect.Value, x interface{}) {
	t := v.Type()
	for k, c := range getNode(x) {
		i, err := strconv.Atoi(k)
		if err != nil {
			panic(k + " is not a valid index for type " + t.String())
		}
		if l := v.Len(); i >= l {
			panic("index is above array size")
		}
		decodeValue(v.Index(i), c)
	}
}

func decodeSlice(v reflect.Value, x interface{}) {
	t := v.Type()
	if t.Elem().Kind() == reflect.Uint8 {
		// Allow, but don't require, byte slices to be encoded as a single string.
		if s, ok := x.(string); ok {
			v.SetBytes([]byte(s))
			return
		}
	}

	for k, c := range getNode(x) {
		i, err := strconv.Atoi(k)
		if err != nil {
			panic(k + " is not a valid index for type " + t.String())
		}
		// "Extend" the slice if it's too short.
		if l := v.Len(); i >= l {
			delta := i - l + 1
			v.Set(reflect.AppendSlice(v, reflect.MakeSlice(t, delta, delta)))
		}
		decodeValue(v.Index(i), c)
	}
}

func decodeBasic(v reflect.Value, x interface{}) {
	t := v.Type()
	s := getString(x)
	if s == "" {
		v.Set(reflect.Zero(t)) // Treat the empty string as the zero value.
		return
	}

	switch k := t.Kind(); k {
	case reflect.Bool:
		if b, e := strconv.ParseBool(s); e == nil {
			v.SetBool(b)
		} else {
			panic("could not parse bool from " + s)
		}
	case reflect.Int,
		reflect.Int8,
		reflect.Int16,
		reflect.Int32,
		reflect.Int64:
		if i, e := strconv.ParseInt(s, 10, 64); e == nil {
			v.SetInt(i)
		} else {
			panic("could not parse int from " + s)
		}
	case reflect.Uint,
		reflect.Uint8,
		reflect.Uint16,
		reflect.Uint32,
		reflect.Uint64:
		if u, e := strconv.ParseUint(s, 10, 64); e == nil {
			v.SetUint(u)
		} else {
			panic("could not parse uint from " + s)
		}
	case reflect.Float32,
		reflect.Float64:
		if f, e := strconv.ParseFloat(s, 64); e == nil {
			v.SetFloat(f)
		} else {
			panic("could not parse float from " + s)
		}
	case reflect.String:
		v.SetString(s)
	default:
		panic(t.String() + " has unsupported kind " + t.Kind().String())
	}
}

func decodeTime(v reflect.Value, x interface{}) {
	t := v.Type()
	s := getString(x)
	if s == "" {
		v.Set(reflect.Zero(v.Type())) // Treat the empty string as the zero value.
		return
	}
	for _, f := range allowedTimeFormats {
		if p, err := time.Parse(f, s); err == nil {
			v.Set(reflect.ValueOf(p).Convert(v.Type()))
			return
		}
	}
	panic("cannot decode string `" + s + "` as " + t.String())
}

// TODO: Find a more efficient way to do this.
var allowedTimeFormats = []string{
	"2006-01-02T15:04:05.999999999Z07:00",
	"2006-01-02T15:04:05.999999999Z07",
	"2006-01-02T15:04:05.999999999Z",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02T15:04:05Z07:00",
	"2006-01-02T15:04:05Z07",
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05",
	"2006-01-02T15:04Z",
	"2006-01-02T15:04",
	"2006-01-02T15Z",
	"2006-01-02T15",
	"2006-01-02",
	"2006-01",
	"2006",
	"15:04:05.999999999Z07:00",
	"15:04:05.999999999Z07",
	"15:04:05.999999999Z",
	"15:04:05.999999999",
	"15:04:05Z07:00",
	"15:04:05Z07",
	"15:04:05Z",
	"15:04:05",
	"15:04Z",
	"15:04",
	"15Z",
	"15",
}