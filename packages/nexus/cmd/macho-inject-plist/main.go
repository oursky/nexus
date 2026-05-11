package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"os"
)

const (
	machoMagic64       = 0xFEEDFACF
	sizeofMachHeader64 = 32
	sizeofSegment64    = 72
	sizeofSection64    = 80
	LC_SEGMENT_64      = 0x19
)

type machHeader64 struct {
	Magic      uint32
	CpuType    uint32
	CpuSubType uint32
	FileType   uint32
	NCmds      uint32
	SizeOfCmds uint32
	Flags      uint32
	Reserved   uint32
}

type segment64 struct {
	Cmd      uint32
	CmdSize  uint32
	SegName  [16]byte
	VMAddr   uint64
	VMSize   uint64
	FileOff  uint64
	FileSize uint64
	MaxProt  uint32
	InitProt uint32
	NSects   uint32
	Flags    uint32
}

type section64 struct {
	SectName  [16]byte
	SegName   [16]byte
	Addr      uint64
	Size      uint64
	Offset    uint32
	Align     uint32
	RelOff    uint32
	NReloc    uint32
	Flags     uint32
	Reserved1 uint32
	Reserved2 uint32
	Reserved3 uint32
}

func main() {
	plistPath := flag.String("plist", "", "path to Info.plist file")
	flag.Parse()

	if *plistPath == "" || flag.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "Usage: %s -plist <plistFile> <machoBinary>\n", os.Args[0])
		os.Exit(1)
	}

	machoPath := flag.Arg(0)

	plistData, err := os.ReadFile(*plistPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading plist: %v\n", err)
		os.Exit(1)
	}

	if len(plistData) == 0 {
		fmt.Fprintf(os.Stderr, "Error: plist file is empty\n")
		os.Exit(1)
	}

	if len(plistData) > 1<<31 {
		fmt.Fprintf(os.Stderr, "Error: plist file too large (%d bytes)\n", len(plistData))
		os.Exit(1)
	}

	fileData, err := os.ReadFile(machoPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading Mach-O binary: %v\n", err)
		os.Exit(1)
	}

	if len(fileData) < sizeofMachHeader64 {
		fmt.Fprintf(os.Stderr, "Error: file too small to be a valid Mach-O binary\n")
		os.Exit(1)
	}

	var hdr machHeader64
	buf := fileData[:sizeofMachHeader64]
	hdr.Magic = binary.LittleEndian.Uint32(buf[0:4])
	hdr.CpuType = binary.LittleEndian.Uint32(buf[4:8])
	hdr.CpuSubType = binary.LittleEndian.Uint32(buf[8:12])
	hdr.FileType = binary.LittleEndian.Uint32(buf[12:16])
	hdr.NCmds = binary.LittleEndian.Uint32(buf[16:20])
	hdr.SizeOfCmds = binary.LittleEndian.Uint32(buf[20:24])
	hdr.Flags = binary.LittleEndian.Uint32(buf[24:28])
	hdr.Reserved = binary.LittleEndian.Uint32(buf[28:32])

	if hdr.Magic != machoMagic64 {
		fmt.Fprintf(os.Stderr, "Error: not a Mach-O 64-bit binary (magic=0x%x)\n", hdr.Magic)
		os.Exit(1)
	}

	var textSegOffset int = -1
	var textSeg segment64
	var linkeditSegOffset int = -1
	var linkeditSeg segment64

	offset := sizeofMachHeader64
	for i := uint32(0); i < hdr.NCmds && offset < len(fileData); i++ {
		if offset+8 > len(fileData) {
			fmt.Fprintf(os.Stderr, "Error: truncated load command\n")
			os.Exit(1)
		}
		cmd := binary.LittleEndian.Uint32(fileData[offset : offset+4])
		cmdSize := binary.LittleEndian.Uint32(fileData[offset+4 : offset+8])

		if cmd == LC_SEGMENT_64 {
			if offset+int(sizeofSegment64) > len(fileData) {
				fmt.Fprintf(os.Stderr, "Error: truncated segment command\n")
				os.Exit(1)
			}
			seg := parseSegment64(fileData[offset:])
			segName := cstring(seg.SegName[:])
			if segName == "__TEXT" {
				textSegOffset = offset
				textSeg = seg
			} else if segName == "__LINKEDIT" {
				linkeditSegOffset = offset
				linkeditSeg = seg
			}
		}

		offset += int(cmdSize)
	}

	if textSegOffset < 0 {
		fmt.Fprintf(os.Stderr, "Error: __TEXT segment not found\n")
		os.Exit(1)
	}

	if linkeditSegOffset < 0 {
		fmt.Fprintf(os.Stderr, "Error: __LINKEDIT segment not found\n")
		os.Exit(1)
	}

	lastSectionEnd := textSeg.VMAddr
	lastSectionEndOffset := uint32(0)

	sectOffset := textSegOffset + sizeofSegment64
	for i := uint32(0); i < textSeg.NSects; i++ {
		if sectOffset+sizeofSection64 > len(fileData) {
			fmt.Fprintf(os.Stderr, "Error: truncated section header\n")
			os.Exit(1)
		}
		sect := parseSection64(fileData[sectOffset:])
		sectEnd := sect.Addr + sect.Size
		sectEndOff := sect.Offset + uint32(sect.Size)
		if sectEnd > lastSectionEnd {
			lastSectionEnd = sectEnd
		}
		if sectEndOff > lastSectionEndOffset {
			lastSectionEndOffset = sectEndOff
		}
		sectOffset += sizeofSection64
	}

	newSectionAddr := lastSectionEnd
	if newSectionAddr == textSeg.VMAddr {
		newSectionAddr = textSeg.VMAddr + textSeg.VMSize
	}

	newSectionOffset := uint32(len(fileData))
	if lastSectionEndOffset > 0 {
		newSectionOffset = lastSectionEndOffset
	}

	newSect := section64{
		Addr:   newSectionAddr,
		Size:   uint64(len(plistData)),
		Offset: newSectionOffset,
	}
	copy(newSect.SectName[:], "__info_plist")
	copy(newSect.SegName[:], "__TEXT")

	newFileSize := len(fileData) + sizeofSection64 + len(plistData)
	if int(newSectionOffset) > len(fileData) {
		newFileSize = int(newSectionOffset) + len(plistData)
	}

	newData := make([]byte, newFileSize)

	shift := sizeofSection64
	copyAmount := textSegOffset + int(textSeg.CmdSize)
	copy(newData[:copyAmount], fileData[:copyAmount])

	copy(newData[copyAmount+shift:], fileData[copyAmount:len(fileData)])

	sectWriteOffset := textSegOffset + sizeofSegment64 + int(textSeg.NSects)*sizeofSection64
	writeSection64(newData[sectWriteOffset:], newSect)

	textSeg.NSects++
	textSeg.CmdSize += uint32(sizeofSection64)

	newSectionVMEnd := newSectionAddr + uint64(len(plistData))
	if newSectionVMEnd > textSeg.VMAddr+textSeg.VMSize {
		textSeg.VMSize = newSectionVMEnd - textSeg.VMAddr
	}

	newSectionFileEnd := uint64(newSectionOffset) + uint64(len(plistData))
	if newSectionFileEnd > textSeg.FileOff+textSeg.FileSize {
		textSeg.FileSize = newSectionFileEnd - textSeg.FileOff
	}

	writeSegment64(newData[textSegOffset:], textSeg)

	if linkeditSegOffset > textSegOffset {
		linkeditSegOffset += shift
	}
	writeSegment64(newData[linkeditSegOffset:], linkeditSeg)

	hdr.SizeOfCmds += uint32(sizeofSection64)
	binary.LittleEndian.PutUint32(newData[16:20], hdr.NCmds)
	binary.LittleEndian.PutUint32(newData[20:24], hdr.SizeOfCmds)

	plistWriteOff := int(newSectionOffset)
	if plistWriteOff+len(plistData) > len(newData) {
		fmt.Fprintf(os.Stderr, "Error: computed file size too small\n")
		os.Exit(1)
	}
	copy(newData[plistWriteOff:], plistData)

	if err := os.WriteFile(machoPath, newData, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing modified binary: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Injected Info.plist (%d bytes) into binary\n", len(plistData))
}

func parseSegment64(b []byte) segment64 {
	var s segment64
	s.Cmd = binary.LittleEndian.Uint32(b[0:4])
	s.CmdSize = binary.LittleEndian.Uint32(b[4:8])
	copy(s.SegName[:], b[8:24])
	s.VMAddr = binary.LittleEndian.Uint64(b[24:32])
	s.VMSize = binary.LittleEndian.Uint64(b[32:40])
	s.FileOff = binary.LittleEndian.Uint64(b[40:48])
	s.FileSize = binary.LittleEndian.Uint64(b[48:56])
	s.MaxProt = binary.LittleEndian.Uint32(b[56:60])
	s.InitProt = binary.LittleEndian.Uint32(b[60:64])
	s.NSects = binary.LittleEndian.Uint32(b[64:68])
	s.Flags = binary.LittleEndian.Uint32(b[68:72])
	return s
}

func writeSegment64(b []byte, s segment64) {
	binary.LittleEndian.PutUint32(b[0:4], s.Cmd)
	binary.LittleEndian.PutUint32(b[4:8], s.CmdSize)
	copy(b[8:24], s.SegName[:])
	binary.LittleEndian.PutUint64(b[24:32], s.VMAddr)
	binary.LittleEndian.PutUint64(b[32:40], s.VMSize)
	binary.LittleEndian.PutUint64(b[40:48], s.FileOff)
	binary.LittleEndian.PutUint64(b[48:56], s.FileSize)
	binary.LittleEndian.PutUint32(b[56:60], s.MaxProt)
	binary.LittleEndian.PutUint32(b[60:64], s.InitProt)
	binary.LittleEndian.PutUint32(b[64:68], s.NSects)
	binary.LittleEndian.PutUint32(b[68:72], s.Flags)
}

func parseSection64(b []byte) section64 {
	var s section64
	copy(s.SectName[:], b[0:16])
	copy(s.SegName[:], b[16:32])
	s.Addr = binary.LittleEndian.Uint64(b[32:40])
	s.Size = binary.LittleEndian.Uint64(b[40:48])
	s.Offset = binary.LittleEndian.Uint32(b[48:52])
	s.Align = binary.LittleEndian.Uint32(b[52:56])
	s.RelOff = binary.LittleEndian.Uint32(b[56:60])
	s.NReloc = binary.LittleEndian.Uint32(b[60:64])
	s.Flags = binary.LittleEndian.Uint32(b[64:68])
	s.Reserved1 = binary.LittleEndian.Uint32(b[68:72])
	s.Reserved2 = binary.LittleEndian.Uint32(b[72:76])
	s.Reserved3 = binary.LittleEndian.Uint32(b[76:80])
	return s
}

func writeSection64(b []byte, s section64) {
	copy(b[0:16], s.SectName[:])
	copy(b[16:32], s.SegName[:])
	binary.LittleEndian.PutUint64(b[32:40], s.Addr)
	binary.LittleEndian.PutUint64(b[40:48], s.Size)
	binary.LittleEndian.PutUint32(b[48:52], s.Offset)
	binary.LittleEndian.PutUint32(b[52:56], s.Align)
	binary.LittleEndian.PutUint32(b[56:60], s.RelOff)
	binary.LittleEndian.PutUint32(b[60:64], s.NReloc)
	binary.LittleEndian.PutUint32(b[64:68], s.Flags)
	binary.LittleEndian.PutUint32(b[68:72], s.Reserved1)
	binary.LittleEndian.PutUint32(b[72:76], s.Reserved2)
	binary.LittleEndian.PutUint32(b[76:80], s.Reserved3)
}

func cstring(b []byte) string {
	for i, v := range b {
		if v == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
