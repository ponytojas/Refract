package fonts

import (
	"encoding/binary"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// Read just the SFNT naming information we need. Collections and malformed
// fonts are left to explicit fontspec file selections. No OS font discovery is
// used, so uploading a family works the same in Docker and on a desktop.
func fontNames(b []byte) (names map[uint16]string, weight uint16, italic bool) {
	if len(b) < 12 || (string(b[:4]) != "\x00\x01\x00\x00" && string(b[:4]) != "OTTO") {
		return nil, 0, false
	}
	count := int(binary.BigEndian.Uint16(b[4:6]))
	if count > (len(b)-12)/16 {
		return nil, 0, false
	}
	var table []byte
	weight = 400
	for i := 0; i < count; i++ {
		record := b[12+i*16 : 28+i*16]
		off, size := uint64(binary.BigEndian.Uint32(record[8:12])), uint64(binary.BigEndian.Uint32(record[12:16]))
		if off+size > uint64(len(b)) {
			return nil, 0, false
		}
		switch string(record[:4]) {
		case "name":
			table = b[off : off+size]
		case "OS/2":
			if size >= 64 {
				weight = binary.BigEndian.Uint16(b[off+4 : off+6])
				italic = binary.BigEndian.Uint16(b[off+62:off+64])&1 != 0
			}
		}
	}
	if len(table) < 6 {
		return nil, 0, false
	}
	count = int(binary.BigEndian.Uint16(table[2:4]))
	storage := int(binary.BigEndian.Uint16(table[4:6]))
	if count > (len(table)-6)/12 {
		return nil, 0, false
	}
	names = make(map[uint16]string)
	for i := 0; i < count; i++ {
		r := table[6+i*12 : 18+i*12]
		platform, encoding, language, id := binary.BigEndian.Uint16(r), binary.BigEndian.Uint16(r[2:]), binary.BigEndian.Uint16(r[4:]), binary.BigEndian.Uint16(r[6:])
		if platform != 0 && (platform != 3 || (encoding != 1 && encoding != 10)) {
			continue
		}
		n, off := int(binary.BigEndian.Uint16(r[8:])), storage+int(binary.BigEndian.Uint16(r[10:]))
		if off > len(table) || n > len(table)-off || n%2 != 0 {
			continue
		}
		units := make([]uint16, n/2)
		for j := range units {
			units[j] = binary.BigEndian.Uint16(table[off+j*2:])
		}
		if names[id] == "" || language == 0x0409 {
			names[id] = string(utf16.Decode(units))
		}
	}
	return names, weight, italic
}

func localFamilies(dir string) (map[string]family, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	found := map[string]family{}
	var total int64
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != dir && (d.Name() == ".git" || d.Name() == ".refract") {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		if !d.Type().IsRegular() || (ext != ".ttf" && ext != ".otf") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > 16<<20 || total+info.Size() > 128<<20 {
			return nil
		}
		total += info.Size()
		rel, _ := filepath.Rel(dir, p)
		b, err := root.ReadFile(rel)
		if err != nil {
			return err
		}
		names, weight, italic := fontNames(b)
		name := names[16]
		if name == "" {
			name = names[1]
		}
		name = strings.ToLower(name)
		if name != "" {
			f := found[name]
			switch {
			case weight == 400 && !italic:
				f.Regular = rel
			case weight == 400 && italic:
				f.Italic = rel
			case weight == 700 && !italic:
				f.Bold = rel
			case weight == 700 && italic:
				f.BoldItalic = rel
			}
			found[name] = f
		}
		if full := names[4]; full != "" && !strings.EqualFold(full, name) {
			found[strings.ToLower(full)] = family{Regular: rel}
		}
		return nil
	})
	return found, err
}
