// Package review is an experimental, explicitly invoked seam for comparing
// reviewers on one frozen evidence snapshot. The package itself never reads
// lane state, transcripts or the network, never writes facts or tasks, and
// gives results no authority: reviewer scores are uncalibrated, not proof. An
// external reviewer the caller configures is the caller's own code and may do
// whatever the user's permissions allow.
package review

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
)

// decodeStrict decodes exactly one JSON value into target, rejecting unknown
// fields, duplicate object keys and trailing data. Field names must match
// exactly: encoding/json alone would also accept "Subject" or "VERSION" and
// let the last of two case variants win.
func decodeStrict(encoded []byte, target any) error {
	if err := rejectDuplicateKeys(encoded); err != nil {
		return err
	}
	if err := requireExactFieldNames(encoded, reflect.TypeOf(target)); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing data after the JSON value")
	}
	return nil
}

// requireExactFieldNames checks that every object decoded into a struct uses
// that struct's JSON names exactly. Raw JSON members (the embedded
// observations) and maps hold data, not seam fields, so their keys are left
// as they are. Shape errors are left for the decoder to report.
func requireExactFieldNames(encoded []byte, typ reflect.Type) error {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	switch {
	case typ.Kind() == reflect.Slice && typ.Elem().Kind() != reflect.Uint8:
		var items []json.RawMessage
		if json.Unmarshal(encoded, &items) != nil {
			return nil
		}
		for _, item := range items {
			if err := requireExactFieldNames(item, typ.Elem()); err != nil {
				return err
			}
		}
	case typ.Kind() == reflect.Struct:
		var members map[string]json.RawMessage
		if json.Unmarshal(encoded, &members) != nil {
			return nil
		}
		fields := map[string]reflect.Type{}
		for index := 0; index < typ.NumField(); index++ {
			field := typ.Field(index)
			if name, _, _ := strings.Cut(field.Tag.Get("json"), ","); field.IsExported() && name != "" && name != "-" {
				fields[name] = field.Type
			}
		}
		for key, value := range members {
			fieldType, known := fields[key]
			if !known {
				return fmt.Errorf("unknown field %q (field names are exact and case-sensitive)", key)
			}
			if err := requireExactFieldNames(value, fieldType); err != nil {
				return err
			}
		}
	}
	return nil
}

func rejectDuplicateKeys(encoded []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	var walk func() error
	walk = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, compound := token.(json.Delim)
		if !compound {
			return nil
		}
		seen := map[string]struct{}{}
		for decoder.More() {
			if delimiter == '{' {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, _ := keyToken.(string)
				if _, exists := seen[key]; exists {
					return fmt.Errorf("duplicate object key %q", key)
				}
				seen[key] = struct{}{}
			}
			if err := walk(); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	}
	return walk()
}

// Canonical returns the canonical bytes of one JSON document: keys sorted, no
// insignificant whitespace, numbers preserved exactly. Equal documents that
// differ only in key order or spacing produce identical bytes.
func Canonical(encoded []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return encodeUnescaped(value)
}

// encodeUnescaped encodes value compactly without HTML escaping, so <, > and
// & stay one byte each and embedded raw JSON keeps those characters as given.
func encodeUnescaped(value any) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(out.Bytes(), "\n"), nil
}

// Hash is the hex sha256 of canonical bytes.
func Hash(canonical []byte) string {
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}
