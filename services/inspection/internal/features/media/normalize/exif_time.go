package normalize

import (
	"bytes"
	"encoding/binary"
	"strings"
	"time"
)

// ExifCaptureTime extracts only the EXIF capture date; GPS and device identifiers are never returned.
func ExifCaptureTime(data []byte) (time.Time, bool) {
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return time.Time{}, false
	}
	for offset := 2; offset+4 <= len(data); {
		if data[offset] != 0xff {
			return time.Time{}, false
		}
		for offset < len(data) && data[offset] == 0xff {
			offset++
		}
		if offset >= len(data) {
			break
		}
		marker := data[offset]
		offset++
		if marker == 0xda || marker == 0xd9 {
			break
		}
		if marker == 0xd8 || marker == 0x01 || (marker >= 0xd0 && marker <= 0xd7) {
			continue
		}
		if offset+2 > len(data) {
			break
		}
		size := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		if size < 2 || offset+size > len(data) {
			break
		}
		segment := data[offset+2 : offset+size]
		offset += size
		if marker == 0xe1 && bytes.HasPrefix(segment, []byte("Exif\x00\x00")) {
			if value, ok := parseExifDate(segment[6:]); ok {
				return value, true
			}
		}
	}
	return time.Time{}, false
}

func parseExifDate(tiff []byte) (time.Time, bool) {
	if len(tiff) < 8 {
		return time.Time{}, false
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return time.Time{}, false
	}
	if order.Uint16(tiff[2:4]) != 42 {
		return time.Time{}, false
	}
	ifd0 := int(order.Uint32(tiff[4:8]))
	exifOffset := 0
	entries := parseIFD(tiff, ifd0, order)
	for _, entry := range entries {
		if entry.tag == 0x8769 && entry.value >= 0 {
			exifOffset = entry.value
		}
	}
	var modification time.Time
	ifd0Date := time.Time{}
	for _, entry := range entries {
		if entry.tag == 0x0132 {
			if value, ok := exifString(tiff, entry); ok {
				if parsed, ok := parseExifDateString(value); ok {
					ifd0Date = parsed
				}
			}
		}
	}
	exifEntries := parseIFD(tiff, exifOffset, order)
	var offset string
	for _, entry := range exifEntries {
		if entry.tag == 0x9003 {
			if value, ok := exifString(tiff, entry); ok {
				if parsed, ok := parseExifDateString(value); ok {
					modification = parsed
				}
			}
		}
		if entry.tag == 0x9011 {
			offset, _ = exifString(tiff, entry)
		}
	}
	if !modification.IsZero() {
		if parsed, ok := parseExifDateWithOffset(tiff, exifEntries, modification, offset); ok {
			return parsed, true
		}
		return modification, true
	}
	if !ifd0Date.IsZero() {
		return ifd0Date, true
	}
	return time.Time{}, false
}

func parseExifDateWithOffset(_ []byte, _ []exifEntry, value time.Time, offset string) (time.Time, bool) {
	if offset == "" {
		return time.Date(value.Year(), value.Month(), value.Day(), value.Hour(), value.Minute(), value.Second(), 0, time.FixedZone("device-local", 0)), true
	}
	zone, err := time.Parse("-07:00", offset)
	if err != nil {
		return time.Time{}, false
	}
	_, seconds := zone.Zone()
	return time.Date(value.Year(), value.Month(), value.Day(), value.Hour(), value.Minute(), value.Second(), 0, time.FixedZone("EXIF", seconds)), true
}

type exifEntry struct {
	tag   uint16
	kind  uint16
	count uint32
	value int
}

func parseIFD(tiff []byte, offset int, order binary.ByteOrder) []exifEntry {
	if offset < 0 || offset+2 > len(tiff) {
		return nil
	}
	count := int(order.Uint16(tiff[offset : offset+2]))
	if count > 256 || offset+2+count*12 > len(tiff) {
		return nil
	}
	entries := make([]exifEntry, 0, count)
	for i := 0; i < count; i++ {
		row := tiff[offset+2+i*12 : offset+14+i*12]
		kind := order.Uint16(row[2:4])
		n := order.Uint32(row[4:8])
		val := int(order.Uint32(row[8:12]))
		entries = append(entries, exifEntry{order.Uint16(row[:2]), kind, n, val})
	}
	return entries
}
func exifString(tiff []byte, entry exifEntry) (string, bool) {
	if entry.kind != 2 || entry.count == 0 || entry.count > 128 {
		return "", false
	}
	offset := entry.value
	if entry.count <= 4 {
		offset = -1
	}
	if offset < 0 || offset+int(entry.count) > len(tiff) {
		return "", false
	}
	value := strings.TrimRight(string(tiff[offset:offset+int(entry.count)]), "\x00")
	return value, value != ""
}
func parseExifDateString(value string) (time.Time, bool) {
	for _, layout := range []string{"2006:01:02 15:04:05", "2006-01-02 15:04:05"} {
		if parsed, err := time.ParseInLocation(layout, value, time.FixedZone("device-local", 0)); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}
