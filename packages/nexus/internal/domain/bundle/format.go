package bundle

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"

	"github.com/klauspost/compress/zstd"
)

// PackMagic is the 8-byte magic number at the start of the footer.
const PackMagic = "NXPACK\x00\x00"

// FooterSize is the fixed size of the NXPACK footer in bytes.
const FooterSize = 64

// packFooterVersion is the current footer version.
const packFooterVersion = 2

// PackFooter represents the 64-byte fixed footer appended to every NXPACK bundle.
//
// Layout (little-endian):
//
//	[0..8)   magic            "NXPACK\x00\x00"
//	[8..12)  version          uint32 = 2
//	[12..20) assets_offset    uint64 (absolute from file start)
//	[20..28) assets_size      uint64
//	[28..32) crc32            uint32 (crc32 of assetsBlob)
//	[32..64) reserved         zeroes
type PackFooter struct {
	Version      uint32
	AssetsOffset uint64
	AssetsSize   uint64
	CRC32        uint32
}

// ToBytes serialises the footer to the 64-byte wire format.
func (f PackFooter) ToBytes() []byte {
	buf := make([]byte, FooterSize)
	copy(buf[0:8], PackMagic)
	binary.LittleEndian.PutUint32(buf[8:12], f.Version)
	binary.LittleEndian.PutUint64(buf[12:20], f.AssetsOffset)
	binary.LittleEndian.PutUint64(buf[20:28], f.AssetsSize)
	binary.LittleEndian.PutUint32(buf[28:32], f.CRC32)
	// [32..64) reserved — already zeroed
	return buf
}

// FromBytes deserialises a PackFooter from a 64-byte array.
// Returns an error if the magic bytes are invalid.
func FromBytes(raw [FooterSize]byte) (PackFooter, error) {
	if string(raw[0:8]) != PackMagic {
		return PackFooter{}, &InvalidBundle{Reason: "invalid NXPACK magic"}
	}
	return PackFooter{
		Version:      binary.LittleEndian.Uint32(raw[8:12]),
		AssetsOffset: binary.LittleEndian.Uint64(raw[12:20]),
		AssetsSize:   binary.LittleEndian.Uint64(raw[20:28]),
		CRC32:        binary.LittleEndian.Uint32(raw[28:32]),
	}, nil
}

// CompressZstd compresses src with zstd and returns the compressed bytes.
// Callers building an assets blob should compress the tar archive first, then
// pass the result to WriteNXPack as assetsBlob.
func CompressZstd(src []byte) ([]byte, error) {
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		return nil, fmt.Errorf("bundle: zstd encoder: %w", err)
	}
	return enc.EncodeAll(src, nil), nil
}

// DecompressZstd decompresses a zstd-compressed byte slice.
func DecompressZstd(src []byte) ([]byte, error) {
	dec, err := zstd.NewReader(nil)
	if err != nil {
		return nil, fmt.Errorf("bundle: zstd decoder: %w", err)
	}
	defer dec.Close()
	out, err := dec.DecodeAll(src, nil)
	if err != nil {
		return nil, fmt.Errorf("bundle: zstd decompress: %w", err)
	}
	return out, nil
}

// WriteNXPack writes a complete NXPACK bundle to w.
//
//   - stubBytes: optional self-executing stub prepended before the assets blob.
//     Pass nil for a plain (non-self-executing) bundle.
//   - assetsBlob: zstd-compressed tar archive containing the bundle assets.
//
// Layout written: [stub] | assetsBlob | footer(64 bytes)
// Footer offsets are absolute from the start of the file.
func WriteNXPack(w io.Writer, assetsBlob []byte, stubBytes []byte) error {
	var assetsOffset uint64

	// Write optional stub.
	if len(stubBytes) > 0 {
		n, err := w.Write(stubBytes)
		if err != nil {
			return fmt.Errorf("bundle: write stub: %w", err)
		}
		assetsOffset = uint64(n)
	}

	// Write assets blob.
	_, err := w.Write(assetsBlob)
	if err != nil {
		return fmt.Errorf("bundle: write assets blob: %w", err)
	}

	// Compute CRC32 over assetsBlob.
	h := crc32.NewIEEE()
	h.Write(assetsBlob)
	checksum := h.Sum32()

	footer := PackFooter{
		Version:      packFooterVersion,
		AssetsOffset: assetsOffset,
		AssetsSize:   uint64(len(assetsBlob)),
		CRC32:        checksum,
	}

	_, err = w.Write(footer.ToBytes())
	if err != nil {
		return fmt.Errorf("bundle: write footer: %w", err)
	}

	return nil
}

// ReadNXPackFooter seeks to the last 64 bytes of r and returns the parsed footer.
func ReadNXPackFooter(r io.ReadSeeker) (PackFooter, error) {
	if _, err := r.Seek(-FooterSize, io.SeekEnd); err != nil {
		return PackFooter{}, fmt.Errorf("bundle: seek to footer: %w", err)
	}
	var raw [FooterSize]byte
	if _, err := io.ReadFull(r, raw[:]); err != nil {
		return PackFooter{}, fmt.Errorf("bundle: read footer: %w", err)
	}
	return FromBytes(raw)
}
