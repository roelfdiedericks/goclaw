package localllm

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func writeGGUFString(b *bytes.Buffer, s string) {
	_ = binary.Write(b, binary.LittleEndian, uint64(len(s)))
	b.WriteString(s)
}

func writeGGUFKV(b *bytes.Buffer, key string, valueType int32, writeValue func(*bytes.Buffer)) {
	writeGGUFString(b, key)
	_ = binary.Write(b, binary.LittleEndian, valueType)
	writeValue(b)
}

func buildMinimalGGUF(kvs []func(*bytes.Buffer)) []byte {
	buf := &bytes.Buffer{}
	buf.WriteString(ggufMagic)
	_ = binary.Write(buf, binary.LittleEndian, uint32(3))
	_ = binary.Write(buf, binary.LittleEndian, uint64(0)) // tensor_count
	_ = binary.Write(buf, binary.LittleEndian, uint64(len(kvs)))
	for _, kv := range kvs {
		kv(buf)
	}
	return buf.Bytes()
}

func TestReadGGUFContextLength_uint64(t *testing.T) {
	data := buildMinimalGGUF([]func(*bytes.Buffer){
		func(b *bytes.Buffer) {
			writeGGUFKV(b, "llama.context_length", ggufTypeUint64, func(w *bytes.Buffer) {
				_ = binary.Write(w, binary.LittleEndian, uint64(12345))
			})
		},
	})
	path := filepath.Join(t.TempDir(), "m.gguf")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	n, found, err := ReadGGUFContextLength(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found || n != 12345 {
		t.Fatalf("got n=%d found=%v", n, found)
	}
}

func TestReadGGUFContextLength_prefersArchitectureKey(t *testing.T) {
	data := buildMinimalGGUF([]func(*bytes.Buffer){
		func(b *bytes.Buffer) {
			writeGGUFKV(b, "general.architecture", ggufTypeString, func(w *bytes.Buffer) {
				writeGGUFString(w, "gemma3")
			})
		},
		func(b *bytes.Buffer) {
			writeGGUFKV(b, "llama.context_length", ggufTypeUint32, func(w *bytes.Buffer) {
				_ = binary.Write(w, binary.LittleEndian, uint32(4096))
			})
		},
		func(b *bytes.Buffer) {
			writeGGUFKV(b, "gemma3.context_length", ggufTypeUint32, func(w *bytes.Buffer) {
				_ = binary.Write(w, binary.LittleEndian, uint32(131072))
			})
		},
	})
	path := filepath.Join(t.TempDir(), "m.gguf")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	n, found, err := ReadGGUFContextLength(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found || n != 131072 {
		t.Fatalf("expected gemma3.context_length 131072, got n=%d found=%v", n, found)
	}
}

func TestReadGGUFContextLength_notFound(t *testing.T) {
	data := buildMinimalGGUF([]func(*bytes.Buffer){
		func(b *bytes.Buffer) {
			writeGGUFKV(b, "general.name", ggufTypeString, func(w *bytes.Buffer) {
				writeGGUFString(w, "test-model")
			})
		},
	})
	path := filepath.Join(t.TempDir(), "m.gguf")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	n, found, err := ReadGGUFContextLength(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found || n != 0 {
		t.Fatalf("expected not found, got n=%d found=%v", n, found)
	}
}

func TestReadGGUFContextLength_rejectsNonGGUF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.bin")
	if err := os.WriteFile(path, []byte("NOTGGUF"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := ReadGGUFContextLength(path)
	if err == nil {
		t.Fatal("expected error for non-GGUF file")
	}
}

func TestPickGGUFContextLength_maxWhenNoArchMatch(t *testing.T) {
	v, ok := pickGGUFContextLength("", map[string]int64{
		"a.context_length": 100,
		"b.context_length": 500,
	})
	if !ok || v != 500 {
		t.Fatalf("expected max 500, got %d ok=%v", v, ok)
	}
}
