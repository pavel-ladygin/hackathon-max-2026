package eventuploads

import (
	"bytes"
	"context"
	_ "embed"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
)

//go:embed testdata/tiny.webp
var tinyWebP []byte

func TestBusyUploadDoesNotBufferRequestBody(t *testing.T) {
	handler := NewHandler(&store.Pool{Pool: &pgxpool.Pool{}}, &memoryObjects{})
	for i := 0; i < cap(handler.decodeSlots); i++ {
		handler.decodeSlots <- struct{}{}
	}
	body := &observedUploadBody{}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/event-submissions/images", body)
	request.Header.Set("Content-Type", "multipart/form-data; boundary=test")
	request = request.WithContext(contracts.WithPrincipal(context.Background(), contracts.Principal{UserID: uuid.New()}))
	response := httptest.NewRecorder()
	handler.upload(response, request)
	if response.Code != http.StatusServiceUnavailable || body.read {
		t.Fatalf("busy upload status=%d body_read=%t, want 503 without buffering", response.Code, body.read)
	}
}

type observedUploadBody struct{ read bool }

func (body *observedUploadBody) Read([]byte) (int, error) {
	body.read = true
	return 0, io.EOF
}

func TestValidateImageAcceptsPNGAndRejectsCorruption(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 3))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	kind, width, height, err := ValidateImage(encoded.Bytes())
	if err != nil || kind != "image/png" || width != 2 || height != 3 {
		t.Fatalf("ValidateImage = %q %dx%d, %v", kind, width, height, err)
	}
	if _, _, _, err := ValidateImage([]byte("not an image")); err == nil {
		t.Fatal("corrupt input was accepted")
	}
}

func TestValidateImageEnforcesPixelLimitBeforeDecode(t *testing.T) {
	data := pngWithDimensions(4001, 10000)
	if _, _, _, err := ValidateImage(data); err == nil {
		t.Fatal("image over 40 MP was accepted")
	}
}

func TestValidateImageFullyDecodesWebPAndRejectsCorruptEntropy(t *testing.T) {
	data := append([]byte(nil), tinyWebP...)
	kind, width, height, err := ValidateImage(data)
	if err != nil || kind != "image/webp" || width != 2 || height != 2 {
		t.Fatalf("ValidateImage = %q %dx%d, %v", kind, width, height, err)
	}
	corruptRejected := false
	for i := 30; i < len(data) && !corruptRejected; i++ { // Preserve RIFF/frame headers; flip compressed data bits.
		for bit := byte(1); bit != 0; bit <<= 1 {
			corrupt := append([]byte(nil), data...)
			corrupt[i] ^= bit
			if _, _, _, err := ValidateImage(corrupt); err != nil {
				corruptRejected = true
				break
			}
		}
	}
	if !corruptRejected {
		t.Fatal("no corrupted WebP entropy mutation was rejected")
	}
}

func pngWithDimensions(width, height uint32) []byte {
	data := append([]byte(nil), []byte("\x89PNG\r\n\x1a\n")...)
	data = append(data, 0, 0, 0, 13, 'I', 'H', 'D', 'R')
	header := make([]byte, 13)
	putUint32BE(header[:4], width)
	putUint32BE(header[4:8], height)
	header[8], header[9], header[10], header[11], header[12] = 8, 6, 0, 0, 0
	data = append(data, header...)
	crc := crc32.ChecksumIEEE(append([]byte("IHDR"), header...))
	crcBytes := make([]byte, 4)
	putUint32BE(crcBytes, crc)
	data = append(data, crcBytes...)
	return append(data, 0, 0, 0, 0, 'I', 'E', 'N', 'D', 0, 0, 0, 0)
}

func putUint32BE(data []byte, value uint32) {
	data[0] = byte(value >> 24)
	data[1] = byte(value >> 16)
	data[2] = byte(value >> 8)
	data[3] = byte(value)
}
