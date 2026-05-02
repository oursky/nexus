package bundle

import (
	"bytes"
	"testing"
)

func TestPackFooterRoundTrip(t *testing.T) {
	original := PackFooter{
		Version:        1,
		AssetsOffset:   512,
		AssetsSize:     1024 * 1024,
		ManifestOffset: 512 + 1024*1024,
		ManifestSize:   4096,
		CRC32:          0xDEADBEEF,
	}

	raw := original.ToBytes()
	if len(raw) != FooterSize {
		t.Fatalf("ToBytes length = %d, want %d", len(raw), FooterSize)
	}
	if string(raw[0:8]) != PackMagic {
		t.Fatalf("magic mismatch: got %q, want %q", string(raw[0:8]), PackMagic)
	}

	var arr [FooterSize]byte
	copy(arr[:], raw)
	got, err := FromBytes(arr)
	if err != nil {
		t.Fatalf("FromBytes error: %v", err)
	}

	if got.Version != original.Version {
		t.Errorf("Version: got %d, want %d", got.Version, original.Version)
	}
	if got.AssetsOffset != original.AssetsOffset {
		t.Errorf("AssetsOffset: got %d, want %d", got.AssetsOffset, original.AssetsOffset)
	}
	if got.AssetsSize != original.AssetsSize {
		t.Errorf("AssetsSize: got %d, want %d", got.AssetsSize, original.AssetsSize)
	}
	if got.ManifestOffset != original.ManifestOffset {
		t.Errorf("ManifestOffset: got %d, want %d", got.ManifestOffset, original.ManifestOffset)
	}
	if got.ManifestSize != original.ManifestSize {
		t.Errorf("ManifestSize: got %d, want %d", got.ManifestSize, original.ManifestSize)
	}
	if got.CRC32 != original.CRC32 {
		t.Errorf("CRC32: got %08x, want %08x", got.CRC32, original.CRC32)
	}
}

func TestFromBytesInvalidMagic(t *testing.T) {
	var arr [FooterSize]byte
	copy(arr[:], "BADMAGIC")
	_, err := FromBytes(arr)
	if err == nil {
		t.Fatal("expected error for invalid magic, got nil")
	}
}

func TestWriteAndExtractNXPack(t *testing.T) {
	assetsBlob := []byte("fake-zstd-compressed-tar-data")
	manifestJSON := []byte(`{"schemaVersion":"2"}`)

	var buf bytes.Buffer
	if err := WriteNXPack(&buf, assetsBlob, manifestJSON, nil); err != nil {
		t.Fatalf("WriteNXPack error: %v", err)
	}

	data := buf.Bytes()
	expectedLen := len(assetsBlob) + len(manifestJSON) + FooterSize
	if len(data) != expectedLen {
		t.Fatalf("output length = %d, want %d", len(data), expectedLen)
	}

	r := bytes.NewReader(data)

	footer, err := ReadNXPackFooter(r)
	if err != nil {
		t.Fatalf("ReadNXPackFooter error: %v", err)
	}
	if footer.Version != 1 {
		t.Errorf("footer.Version = %d, want 1", footer.Version)
	}
	if footer.AssetsOffset != 0 {
		t.Errorf("footer.AssetsOffset = %d, want 0", footer.AssetsOffset)
	}
	if footer.AssetsSize != uint64(len(assetsBlob)) {
		t.Errorf("footer.AssetsSize = %d, want %d", footer.AssetsSize, len(assetsBlob))
	}
	if footer.ManifestOffset != uint64(len(assetsBlob)) {
		t.Errorf("footer.ManifestOffset = %d, want %d", footer.ManifestOffset, len(assetsBlob))
	}
	if footer.ManifestSize != uint64(len(manifestJSON)) {
		t.Errorf("footer.ManifestSize = %d, want %d", footer.ManifestSize, len(manifestJSON))
	}

	r.Seek(0, 0)
	got, err := ExtractNXPackManifest(r)
	if err != nil {
		t.Fatalf("ExtractNXPackManifest error: %v", err)
	}
	if !bytes.Equal(got, manifestJSON) {
		t.Errorf("manifest mismatch: got %q, want %q", got, manifestJSON)
	}
}

func TestWriteNXPackWithStub(t *testing.T) {
	stub := []byte("#!/bin/sh\nexec self\n")
	assetsBlob := []byte("compressed-assets")
	manifestJSON := []byte(`{}`)

	var buf bytes.Buffer
	if err := WriteNXPack(&buf, assetsBlob, manifestJSON, stub); err != nil {
		t.Fatalf("WriteNXPack with stub error: %v", err)
	}

	r := bytes.NewReader(buf.Bytes())
	footer, err := ReadNXPackFooter(r)
	if err != nil {
		t.Fatalf("ReadNXPackFooter error: %v", err)
	}

	if footer.AssetsOffset != uint64(len(stub)) {
		t.Errorf("AssetsOffset with stub = %d, want %d", footer.AssetsOffset, len(stub))
	}
	if footer.ManifestOffset != uint64(len(stub)+len(assetsBlob)) {
		t.Errorf("ManifestOffset with stub = %d, want %d", footer.ManifestOffset, len(stub)+len(assetsBlob))
	}
}
