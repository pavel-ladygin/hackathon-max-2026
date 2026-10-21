package eventuploads

import (
	"bytes"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	_ "golang.org/x/image/webp"
)

const (
	MaxImageBytes   = 10 << 20
	MaxImagePixels  = 40000000
	maxRequestBytes = MaxImageBytes + (1 << 20)
)

type Handler struct {
	service     *Service
	decodeSlots chan struct{}
}

func NewHandler(db *store.Pool, objects ObjectStore) *Handler {
	return &Handler{service: NewService(db, objects), decodeSlots: make(chan struct{}, 2)}
}

func (h *Handler) RegisterRoutes(r chi.Router, authenticate func(http.Handler) http.Handler) {
	if authenticate == nil {
		return
	}
	// The submission handler already owns the /api/v1 subrouter. Register the
	// full upload path directly so both handlers can share that API prefix.
	r.With(authenticate).Post("/api/v1/event-submissions/images", h.upload)
}

type uploadResponse struct {
	ImageID uuid.UUID `json:"image_id"`
}

func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	principal, ok := contracts.PrincipalFromContext(r.Context())
	if !ok {
		writeUploadError(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication is required.")
		return
	}
	if h.service == nil || !h.service.Available() {
		writeUploadError(w, r, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "Image storage is not available.")
		return
	}
	if r.ContentLength > maxRequestBytes {
		writeUploadError(w, r, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "The image must be no larger than 10 MiB.")
		return
	}
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		writeUploadError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Upload one image using multipart form data.")
		return
	}
	// Bound the whole buffered upload, not just decoding: waiting requests must
	// not each retain another 10 MiB image while the decoder slots are occupied.
	select {
	case h.decodeSlots <- struct{}{}:
		defer func() { <-h.decodeSlots }()
	default:
		w.Header().Set("Retry-After", "2")
		writeUploadError(w, r, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "Image upload is busy. Please try again.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	reader := multipart.NewReader(r.Body, params["boundary"])
	var data []byte
	fileCount := 0
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				writeUploadError(w, r, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "The image must be no larger than 10 MiB.")
			} else {
				writeUploadError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "The image upload is malformed.")
			}
			return
		}
		if part.FormName() != "file" || part.FileName() == "" || fileCount > 0 {
			part.Close()
			writeUploadError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Upload exactly one image in the file field.")
			return
		}
		fileCount++
		data, err = io.ReadAll(io.LimitReader(part, MaxImageBytes+1))
		part.Close()
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				writeUploadError(w, r, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "The image must be no larger than 10 MiB.")
			} else {
				writeUploadError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "The image could not be read.")
			}
			return
		}
		if len(data) > MaxImageBytes {
			writeUploadError(w, r, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "The image must be no larger than 10 MiB.")
			return
		}
	}
	if fileCount != 1 || len(data) == 0 {
		writeUploadError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Choose one image to upload.")
		return
	}
	contentType, width, height, err := ValidateImage(data)
	if err != nil {
		writeUploadError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "Choose a valid JPEG, PNG, or WebP image up to 10 MiB and 40 megapixels.")
		return
	}
	stored, err := h.service.Store(r.Context(), principal.UserID, data, contentType, width, height)
	if err != nil {
		if errors.Is(err, ErrUnavailable) {
			writeUploadError(w, r, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "Image storage is not available.")
		} else if errors.Is(err, ErrQuotaExceeded) {
			writeUploadError(w, r, http.StatusTooManyRequests, "RATE_LIMITED", "Remove an unused image or try again later.")
		} else {
			writeUploadError(w, r, http.StatusBadGateway, "IMAGE_UPLOAD_FAILED", "The image could not be saved. Please try again.")
		}
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, uploadResponse{ImageID: stored.ID})
}

func writeUploadError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	httpapi.WriteError(w, httpapi.RequestID(r.Context()), httpapi.Error{Status: status, Code: code, Message: message})
}

// ValidateImage checks the actual image signature and decoder validity before
// returning dimensions and the canonical MIME type. Dimensions are checked
// before raster decode to bound memory consumption.
func ValidateImage(data []byte) (string, int, int, error) {
	if len(data) == 0 || len(data) > MaxImageBytes {
		return "", 0, 0, errors.New("image must be at most 10 MiB")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", 0, 0, errors.New("file is not a valid JPEG or PNG image")
	}
	if format != "jpeg" && format != "png" && format != "webp" {
		return "", 0, 0, errors.New("only JPEG, PNG and WebP images are accepted")
	}
	if !validDimensions(config.Width, config.Height) {
		return "", 0, 0, errors.New("image exceeds 40 megapixels")
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return "", 0, 0, errors.New("image data is corrupt")
	}
	if format == "jpeg" {
		return "image/jpeg", config.Width, config.Height, nil
	}
	if format == "webp" {
		return "image/webp", config.Width, config.Height, nil
	}
	return "image/png", config.Width, config.Height, nil
}

func validDimensions(width, height int) bool {
	return width > 0 && height > 0 && width <= MaxImagePixels/height
}
