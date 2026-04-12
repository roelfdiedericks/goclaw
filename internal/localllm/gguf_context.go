package localllm

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	ggufMagic = "GGUF"

	// ggufMetadataScanLimit caps how much of a file we read while walking KV metadata.
	ggufMetadataScanLimit = 32 << 20

	maxGGUFKeyLen    = 1 << 16
	maxGGUFStringLen = 16 << 20
)

// GGUF metadata value types (llama.cpp / ggml), wire enum is int32.
const (
	ggufTypeUint8   = 0
	ggufTypeInt8    = 1
	ggufTypeUint16  = 2
	ggufTypeInt16   = 3
	ggufTypeUint32  = 4
	ggufTypeInt32   = 5
	ggufTypeFloat32 = 6
	ggufTypeBool    = 7
	ggufTypeString  = 8
	ggufTypeArray   = 9
	ggufTypeUint64  = 10
	ggufTypeInt64   = 11
	ggufTypeFloat64 = 12
)

// ReadGGUFContextLength reads architecture-specific context_length from a GGUF file's metadata.
// It returns found=false when the file parses as GGUF but no usable context_length key exists.
func ReadGGUFContextLength(path string) (n int, found bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false, err
	}
	defer f.Close()

	var hdr [24]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return 0, false, fmt.Errorf("read gguf header: %w", err)
	}
	if string(hdr[0:4]) != ggufMagic {
		return 0, false, fmt.Errorf("not a GGUF file")
	}
	version := binary.LittleEndian.Uint32(hdr[4:8])
	if version < 2 || version > 3 {
		return 0, false, fmt.Errorf("unsupported GGUF version %d", version)
	}
	_ = binary.LittleEndian.Uint64(hdr[8:16]) // tensor_count — not needed for KV scan
	kvCount := binary.LittleEndian.Uint64(hdr[16:24])

	lr := io.LimitedReader{R: f, N: ggufMetadataScanLimit}
	br := bufio.NewReader(&lr)

	var arch string
	candidates := make(map[string]int64)

	for i := uint64(0); i < kvCount; i++ {
		key, err := readGGUFString(br, maxGGUFKeyLen)
		if err != nil {
			return 0, false, fmt.Errorf("gguf kv[%d] key: %w", i, err)
		}
		var vt int32
		if err := binary.Read(br, binary.LittleEndian, &vt); err != nil {
			return 0, false, fmt.Errorf("gguf kv[%d] type: %w", i, err)
		}

		switch {
		case key == "general.architecture" && vt == ggufTypeString:
			s, err := readGGUFString(br, maxGGUFStringLen)
			if err != nil {
				return 0, false, fmt.Errorf("general.architecture: %w", err)
			}
			arch = strings.TrimSpace(s)
		case strings.HasSuffix(key, ".context_length") && isGGUFIntScalar(vt):
			v, err := readGGUFIntScalar(br, vt)
			if err != nil {
				return 0, false, fmt.Errorf("read %s: %w", key, err)
			}
			if v > 0 {
				candidates[key] = v
			}
		default:
			if err := skipGGUFValue(br, vt); err != nil {
				return 0, false, fmt.Errorf("skip gguf kv %q: %w", key, err)
			}
		}
	}

	v, ok := pickGGUFContextLength(arch, candidates)
	if !ok || v <= 0 {
		return 0, false, nil
	}
	if v > int64(^uint(0)>>1) {
		return 0, false, fmt.Errorf("context_length overflows int")
	}
	return int(v), true, nil
}

func pickGGUFContextLength(arch string, candidates map[string]int64) (int64, bool) {
	if len(candidates) == 0 {
		return 0, false
	}
	if arch != "" {
		if v, ok := candidates[arch+".context_length"]; ok && v > 0 {
			return v, true
		}
	}
	var best int64
	for _, v := range candidates {
		if v > best {
			best = v
		}
	}
	return best, best > 0
}

func isGGUFIntScalar(vt int32) bool {
	switch vt {
	case ggufTypeUint8, ggufTypeInt8, ggufTypeUint16, ggufTypeInt16,
		ggufTypeUint32, ggufTypeInt32, ggufTypeUint64, ggufTypeInt64:
		return true
	default:
		return false
	}
}

func readGGUFIntScalar(br io.Reader, vt int32) (int64, error) {
	switch vt {
	case ggufTypeUint8:
		var v uint8
		if err := binary.Read(br, binary.LittleEndian, &v); err != nil {
			return 0, err
		}
		return int64(v), nil
	case ggufTypeInt8:
		var v int8
		if err := binary.Read(br, binary.LittleEndian, &v); err != nil {
			return 0, err
		}
		return int64(v), nil
	case ggufTypeUint16:
		var v uint16
		if err := binary.Read(br, binary.LittleEndian, &v); err != nil {
			return 0, err
		}
		return int64(v), nil
	case ggufTypeInt16:
		var v int16
		if err := binary.Read(br, binary.LittleEndian, &v); err != nil {
			return 0, err
		}
		return int64(v), nil
	case ggufTypeUint32:
		var v uint32
		if err := binary.Read(br, binary.LittleEndian, &v); err != nil {
			return 0, err
		}
		return int64(v), nil
	case ggufTypeInt32:
		var v int32
		if err := binary.Read(br, binary.LittleEndian, &v); err != nil {
			return 0, err
		}
		return int64(v), nil
	case ggufTypeUint64:
		var v uint64
		if err := binary.Read(br, binary.LittleEndian, &v); err != nil {
			return 0, err
		}
		return int64(v), nil
	case ggufTypeInt64:
		var v int64
		if err := binary.Read(br, binary.LittleEndian, &v); err != nil {
			return 0, err
		}
		return v, nil
	default:
		return 0, fmt.Errorf("not an integer scalar type %d", vt)
	}
}

func readGGUFString(br *bufio.Reader, maxLen uint64) (string, error) {
	var n uint64
	if err := binary.Read(br, binary.LittleEndian, &n); err != nil {
		return "", err
	}
	if n > maxLen {
		return "", fmt.Errorf("gguf string length %d exceeds limit", n)
	}
	if n == 0 {
		return "", nil
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(br, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func skipGGUFValue(br *bufio.Reader, vt int32) error {
	switch vt {
	case ggufTypeArray:
		var elem int32
		if err := binary.Read(br, binary.LittleEndian, &elem); err != nil {
			return err
		}
		var count uint64
		if err := binary.Read(br, binary.LittleEndian, &count); err != nil {
			return err
		}
		const maxArrayElems = 1 << 24
		if count > maxArrayElems {
			return fmt.Errorf("gguf array too large: %d", count)
		}
		for i := uint64(0); i < count; i++ {
			if err := skipGGUFValue(br, elem); err != nil {
				return err
			}
		}
		return nil
	case ggufTypeString:
		s, err := readGGUFString(br, maxGGUFStringLen)
		if err != nil {
			return err
		}
		_ = s
		return nil
	default:
		sz := ggufScalarTypeSize(vt)
		if sz < 0 {
			return fmt.Errorf("unknown gguf value type %d", vt)
		}
		if _, err := io.CopyN(io.Discard, br, sz); err != nil {
			return err
		}
		return nil
	}
}

func ggufScalarTypeSize(vt int32) int64 {
	switch vt {
	case ggufTypeUint8, ggufTypeInt8, ggufTypeBool:
		return 1
	case ggufTypeUint16, ggufTypeInt16:
		return 2
	case ggufTypeUint32, ggufTypeInt32, ggufTypeFloat32:
		return 4
	case ggufTypeUint64, ggufTypeInt64, ggufTypeFloat64:
		return 8
	default:
		return -1
	}
}
