package timepad

import "testing"

func TestNormalizeImagesRepairsMalformedTimepadPosterURL(t *testing.T) {
	const uuid = "616e34b0-0b30-4a3a-8d5a-3f9b0cabe260"
	const malformed = "https://ucare.timepad.ru/616e34b0-0b30-4a3a-8d5a-3f9b0cabe260/-/preview/308x600/-/format/jpeg/-/format/jpeg/poster_org_418499.jpg/-/preview/308x600/-/format/jpeg/poster_event_420945.jpg"
	images := normalizeImages(imageDTO{DefaultURL: malformed})
	if len(images) != 1 {
		t.Fatalf("expected one image, got %d", len(images))
	}
	want := "https://ucare.timepad.ru/" + uuid + "/"
	if images[0].URL != want {
		t.Fatalf("repaired URL = %q, want %q", images[0].URL, want)
	}
}

func TestNormalizeImagesUsesOriginalForValidTimepadPosterURL(t *testing.T) {
	const valid = "https://ucare.timepad.ru/616e34b0-b30a-4a3a-8d5a-3f9b0cabe260/-/preview/308x600/-/format/jpeg/poster_event_420945.jpg"
	images := normalizeImages(imageDTO{DefaultURL: valid})
	if len(images) != 1 {
		t.Fatalf("expected one image, got %d", len(images))
	}
	const want = "https://ucare.timepad.ru/616e34b0-b30a-4a3a-8d5a-3f9b0cabe260/"
	if images[0].URL != want {
		t.Fatalf("image URL = %q, want original %q", images[0].URL, want)
	}
}

func TestNormalizeImagesDoesNotRepairMalformedURLOnOtherHost(t *testing.T) {
	const url = "https://cdn.example.test/616e34b0-b30a-4a3a-8d5a-3f9b0cabe260/-/preview/308x600/-/format/jpeg/poster_org_418499.jpg/-/preview/308x600/-/format/jpeg/poster_event_420945.jpg"
	images := normalizeImages(imageDTO{DefaultURL: url})
	if len(images) != 1 {
		t.Fatalf("expected one image, got %d", len(images))
	}
	if images[0].URL != url {
		t.Fatalf("non-Timepad URL changed to %q", images[0].URL)
	}
}
