package build

import (
	"bytes"
	"debug/macho"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/xgo-dev/llgo/xtool/ar"
)

const maxStaticMapNativeInputBytes = 256 << 20

type staticMapObjectDigest struct {
	SHA256 string
	Bytes  int64
}

// staticMapNativeInventory describes actual native input bytes. It is not an
// omission certificate: Go references, export policy, final command and every
// other linker input still need their independent proofs.
type staticMapNativeInventory struct {
	SHA256     string
	Members    int
	OwnObjects int
	Mentions   []string
}

func staticMapReadNativeInput(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxStaticMapNativeInputBytes+1))
	if err == nil && len(data) > maxStaticMapNativeInputBytes {
		err = fmt.Errorf("static map native input exceeds its inventory budget")
	}
	return data, err
}

// Darwin compiler-rt is a universal file whose slices are ar archives. The
// debug/macho fat reader expects Mach-O images inside those slices instead.
// Select only the exact generic ARM64 subtype used by this native pipeline.
func staticMapArm64Slice(data []byte) ([]byte, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("truncated native input")
	}
	magic := binary.BigEndian.Uint32(data[:4])
	if magic != 0xcafebabe && magic != 0xcafebabf {
		return data, nil
	}
	count := uint64(binary.BigEndian.Uint32(data[4:8]))
	width := uint64(20)
	if magic == 0xcafebabf {
		width = 32
	}
	if count == 0 || count > 64 || 8+count*width > uint64(len(data)) {
		return nil, fmt.Errorf("invalid universal native input header")
	}
	var selected []byte
	type extent struct{ start, end uint64 }
	var extents []extent
	for i := uint64(0); i < count; i++ {
		header := data[8+i*width : 8+(i+1)*width]
		cpu, subtype := binary.BigEndian.Uint32(header[:4]), binary.BigEndian.Uint32(header[4:8])
		start, size := uint64(binary.BigEndian.Uint32(header[8:12])), uint64(binary.BigEndian.Uint32(header[12:16]))
		alignment := binary.BigEndian.Uint32(header[16:20])
		if width == 32 {
			start, size = binary.BigEndian.Uint64(header[8:16]), binary.BigEndian.Uint64(header[16:24])
			alignment = binary.BigEndian.Uint32(header[24:28])
			if binary.BigEndian.Uint32(header[28:32]) != 0 {
				return nil, fmt.Errorf("nonzero universal input reserved field")
			}
		}
		if size == 0 || start < 8+count*width || start > uint64(len(data)) || size > uint64(len(data))-start ||
			alignment > 31 || start%(uint64(1)<<alignment) != 0 {
			return nil, fmt.Errorf("invalid universal native input slice")
		}
		for _, other := range extents {
			if start < other.end && other.start < start+size {
				return nil, fmt.Errorf("overlapping universal native input slices")
			}
		}
		extents = append(extents, extent{start, start + size})
		if cpu == uint32(macho.CpuArm64) && subtype == 0 {
			if selected != nil {
				return nil, fmt.Errorf("ambiguous ARM64 native input slice")
			}
			selected = data[start : start+size]
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("universal native input lacks the exact ARM64 slice")
	}
	return selected, nil
}

func staticMapMachOObjectMentions(data []byte) ([]string, error) {
	object, err := macho.NewFile(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer object.Close()
	if object.Type != macho.TypeObj || object.Cpu != macho.CpuArm64 || object.SubCpu != 0 || object.Symtab == nil {
		return nil, fmt.Errorf("native input is not an inventoried ARM64 object")
	}
	for _, load := range object.Loads {
		raw := load.Raw()
		if len(raw) < 8 {
			return nil, fmt.Errorf("truncated native object load command")
		}
		command := object.ByteOrder.Uint32(raw[:4])
		switch command {
		case 0x2, 0xb, 0x19, 0x1b, 0x24, 0x26, 0x29, 0x2a, 0x2e, 0x32:
			// Symbol/segment tables, build/source versions, UUID, function/data
			// ranges and ARM64 optimization hints do not introduce link inputs.
		default:
			// In particular LC_LINKER_OPTION may silently load another archive.
			return nil, fmt.Errorf("uninventoried native object load command %#x", command)
		}
	}
	mentions := make([]string, 0, len(object.Symtab.Syms))
	for _, symbol := range object.Symtab.Syms {
		if symbol.Type&0xe0 == 0 && symbol.Type&0x0e == 0x0a {
			// N_INDR stores its target in the string table rather than Name.
			return nil, fmt.Errorf("native indirect symbol requires a separate alias proof")
		}
		if symbol.Name != "" {
			mentions = append(mentions, symbol.Name)
		}
	}
	return mentions, nil
}

func inspectStaticMapNativeArchive(path string, own []staticMapObjectDigest) (*staticMapNativeInventory, error) {
	data, err := staticMapReadNativeInput(path)
	if err != nil {
		return nil, err
	}
	return inspectStaticMapNativeArchiveBytes(data, own)
}

func inspectStaticMapNativeArchiveBytes(data []byte, own []staticMapObjectDigest) (*staticMapNativeInventory, error) {
	if len(data) > maxStaticMapNativeInputBytes {
		return nil, fmt.Errorf("oversized native archive")
	}
	result := &staticMapNativeInventory{SHA256: digestBytes(data), Mentions: []string{}}
	slice, err := staticMapArm64Slice(data)
	if err != nil {
		return nil, err
	}
	if err := validateStaticMapArchiveFraming(slice); err != nil {
		return nil, err
	}
	reader, err := ar.NewReader(bytes.NewReader(slice))
	if err != nil {
		return nil, err
	}
	seenOwn := make(map[string]bool)
	for _, object := range own {
		_, duplicate := seenOwn[object.SHA256]
		if !staticMapInitDigestValid(object.SHA256) || object.Bytes <= 0 || object.Bytes > maxStaticMapNativeInputBytes || duplicate {
			return nil, fmt.Errorf("invalid or duplicate compiler-owned object digest")
		}
		seenOwn[object.SHA256] = false
	}
	mentions := make(map[string]bool)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if header.Size < 0 || header.Size > maxStaticMapNativeInputBytes {
			return nil, fmt.Errorf("invalid native archive member size")
		}
		name := strings.TrimRight(header.Name, "/ \x00")
		member, err := io.ReadAll(reader)
		if err != nil || int64(len(member)) != header.Size {
			return nil, fmt.Errorf("truncated native archive member %q", name)
		}
		if name == "__.SYMDEF" || name == "__.SYMDEF SORTED" || name == "__.SYMDEF_64" || name == "__.SYMDEF_64 SORTED" || name == "" && header.Name == "/" {
			continue
		}
		result.Members++
		matched := false
		for _, object := range own {
			if int64(len(member)) < object.Bytes || int64(len(member))-object.Bytes > 7 ||
				len(bytes.Trim(member[object.Bytes:], "\n")) != 0 || digestBytes(member[:object.Bytes]) != object.SHA256 {
				continue
			}
			if seenOwn[object.SHA256] {
				return nil, fmt.Errorf("compiler-owned object appears more than once")
			}
			seenOwn[object.SHA256] = true
			matched = true
			result.OwnObjects++
			break
		}
		if matched {
			continue
		}
		memberMentions, err := staticMapMachOObjectMentions(member)
		if err != nil {
			return nil, fmt.Errorf("native member %q: %w", name, err)
		}
		for _, symbol := range memberMentions {
			mentions[symbol] = true
		}
	}
	for _, found := range seenOwn {
		if !found {
			return nil, fmt.Errorf("compiler-owned object is missing from archive")
		}
	}
	for symbol := range mentions {
		result.Mentions = append(result.Mentions, symbol)
	}
	sort.Strings(result.Mentions)
	return result, nil
}

// Validate framing before the shared reader allocates an extended BSD member
// name. This also prevents a seek past a truncated final member from looking
// like a successful end of archive.
func validateStaticMapArchiveFraming(data []byte) error {
	if !bytes.HasPrefix(data, []byte("!<arch>\n")) {
		return fmt.Errorf("unsupported native archive format")
	}
	for pos, members := 8, 0; pos < len(data); members++ {
		if members >= 100000 || len(data)-pos < 60 {
			return fmt.Errorf("invalid native archive framing")
		}
		header := data[pos : pos+60]
		if string(header[58:60]) != "`\n" {
			return fmt.Errorf("invalid native archive member header")
		}
		size, err := strconv.ParseUint(strings.TrimSpace(string(header[48:58])), 10, 64)
		if err != nil || size > uint64(len(data)-pos-60) {
			return fmt.Errorf("native archive member extends beyond input")
		}
		name := strings.TrimSpace(string(header[:16]))
		if strings.HasPrefix(name, "#1/") {
			length, err := strconv.ParseUint(name[3:], 10, 64)
			if err != nil || length > 4096 || length > size {
				return fmt.Errorf("invalid native archive extended name")
			}
		}
		pos += 60 + int(size)
		if size%2 != 0 {
			if pos >= len(data) || data[pos] != '\n' {
				return fmt.Errorf("invalid native archive member padding")
			}
			pos++
		}
	}
	return nil
}
