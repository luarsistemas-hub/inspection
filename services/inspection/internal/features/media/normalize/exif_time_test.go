package normalize

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

func TestExifCaptureTimePrefersOriginalAndUsesOffset(t *testing.T) {
	got, ok := ExifCaptureTime(exifJPEG("2020:01:02 03:04:05", "2021:06:07 12:13:14", "+02:00"))
	if !ok || !got.Equal(time.Date(2021, 6, 7, 10, 13, 14, 0, time.UTC)) {
		t.Fatalf("capture time=%v ok=%v", got, ok)
	}
}

func TestExifCaptureTimeWithoutOffsetKeepsDeviceLocalWallClock(t *testing.T) {
	got, ok := ExifCaptureTime(exifJPEG("2020:01:02 03:04:05", "2021:06:07 12:13:14", ""))
	if !ok || got.Format("15:04:05") != "12:13:14" || got.Location().String() != "device-local" {
		t.Fatalf("local capture time=%v ok=%v", got, ok)
	}
}

func TestExifCaptureTimeRejectsMalformedDateAndDoesNotReturnGPS(t *testing.T) {
	if _, ok := ExifCaptureTime(exifJPEG("not-a-date", "still-not-a-date", "+02:00")); ok {
		t.Fatal("malformed original EXIF date accepted")
	}
	if got, ok := ExifCaptureTime([]byte("not jpeg")); ok || !got.IsZero() {
		t.Fatalf("non-JPEG payload exposed EXIF: %v %v", got, ok)
	}
}

func exifJPEG(modification, original, offset string) []byte {
	const ifd0Offset, exifOffset = 8, 38
	data := make([]byte, 128)
	copy(data, "II")
	binary.LittleEndian.PutUint16(data[2:], 42)
	binary.LittleEndian.PutUint32(data[4:], ifd0Offset)
	binary.LittleEndian.PutUint16(data[ifd0Offset:], 2)
	entry := data[ifd0Offset+2:]
	binary.LittleEndian.PutUint16(entry, 0x0132)
	binary.LittleEndian.PutUint16(entry[2:], 2)
	binary.LittleEndian.PutUint32(entry[4:], uint32(len(modification)+1))
	binary.LittleEndian.PutUint32(entry[8:], 70)
	entry = data[ifd0Offset+14:]
	binary.LittleEndian.PutUint16(entry, 0x8769)
	binary.LittleEndian.PutUint16(entry[2:], 4)
	binary.LittleEndian.PutUint32(entry[4:], 1)
	binary.LittleEndian.PutUint32(entry[8:], exifOffset)
	binary.LittleEndian.PutUint16(data[exifOffset:], 2)
	entry = data[exifOffset+2:]
	binary.LittleEndian.PutUint16(entry, 0x9003)
	binary.LittleEndian.PutUint16(entry[2:], 2)
	binary.LittleEndian.PutUint32(entry[4:], uint32(len(original)+1))
	binary.LittleEndian.PutUint32(entry[8:], 90)
	entry = data[exifOffset+14:]
	binary.LittleEndian.PutUint16(entry, 0x9011)
	binary.LittleEndian.PutUint16(entry[2:], 2)
	binary.LittleEndian.PutUint32(entry[4:], uint32(len(offset)+1))
	binary.LittleEndian.PutUint32(entry[8:], 110)
	copy(data[70:], modification+"\x00")
	copy(data[90:], original+"\x00")
	copy(data[110:], offset+"\x00")
	tiff := data[:]
	segment := append([]byte("Exif\x00\x00"), tiff...)
	var jpeg bytes.Buffer
	jpeg.Write([]byte{0xff, 0xd8, 0xff, 0xe1})
	_ = binary.Write(&jpeg, binary.BigEndian, uint16(len(segment)+2))
	jpeg.Write(segment)
	jpeg.Write([]byte{0xff, 0xd9})
	return jpeg.Bytes()
}
