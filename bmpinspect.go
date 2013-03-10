// ◄◄◄ bmpinspect ►►►
// 
// A program to inspect the contents of a Windows BMP file.
// Copyright (c) 2012 Jason Summers

package main

import "errors"
import "fmt"
import "os"
import "io/ioutil"
import "encoding/binary"

const (
	bI_RGB       = 0
	bI_RLE8      = 1
	bI_RLE4      = 2
	bI_BITFIELDS = 3
	bI_JPEG      = 4
	bI_PNG       = 5
	// Some sources say 4, but 6 is confirmed by the Windows CE SDK.
	bI_ALPHABITFIELDS = 6
	bI_SRCPREROTATE   = 0x8000 // Unconfirmed.
)

var cmprNames = map[uint32]string{
	bI_RGB:            "BI_RGB (uncompressed)",
	bI_RLE8:           "BI_RLE8",
	bI_RLE4:           "BI_RLE4",
	bI_BITFIELDS:      "BI_BITFIELDS (uncompressed)",
	bI_JPEG:           "BI_JPEG",
	bI_PNG:            "BI_PNG",
	bI_ALPHABITFIELDS: "BI_ALPHABITFIELDS (uncompressed)",
}

const (
	lCS_CALIBRATED_RGB      = 0
	lCS_sRGB                = 0x73524742
	lCS_WINDOWS_COLOR_SPACE = 0x57696e20
	pROFILE_LINKED          = 0x4c494e4b
	pROFILE_EMBEDDED        = 0x4d424544
)

var csTypeNames = map[uint32]string{
	lCS_CALIBRATED_RGB:      "LCS_CALIBRATED_RGB",
	1:                       "LCS_DEVICE_RGB (?)",
	2:                       "LCS_DEVICE_CMYK (?)",
	lCS_sRGB:                "LCS_sRGB",
	lCS_WINDOWS_COLOR_SPACE: "LCS_WINDOWS_COLOR_SPACE",
	pROFILE_LINKED:          "PROFILE_LINKED",
	pROFILE_EMBEDDED:        "PROFILE_EMBEDDED",
}

var intentNames = map[uint32]string{
	1: "LCS_GM_BUSINESS (Saturation)",
	2: "LCS_GM_GRAPHICS (Relative)",
	4: "LCS_GM_IMAGES (Perceptual)",
	8: "LCS_GM_ABS_COLORIMETRIC",
}

type versionInfo_type struct {
	id                    string
	prefix                string
	name                  string
	inspectInfoheaderFunc func(ctx *ctx_type, d []byte) error
}

// Information about the different BMP versions, keyed off of the number of
// bytes in the InfoHeader.
var versionInfo = map[uint32]versionInfo_type{
	12:  {"os2", "bc", "OS/2-style", inspectInfoheaderOS2},
	40:  {"3", "bi", "version 3", inspectInfoheaderV3},
	52:  {"", "", "BITMAPV2INFOHEADER", nil},
	56:  {"", "", "BITMAPV3INFOHEADER", nil},
	64:  {"os2V2", "", "OS/2 2.0", inspectInfoheaderOS2V2},
	108: {"4", "bV4", "version 4", inspectInfoheaderV4},
	124: {"5", "bV5", "version 5", inspectInfoheaderV5},
}

type ctx_type struct {
	fileName string
	data     []byte
	fileSize int64
	pos      int64

	printPixels bool

	bmpVerID string // Version name used by bmpinspect: "os2", "3", "4", "5"
	bitCount int

	imgWidth       int
	imgHeight      int
	bfOffBits      uint32
	infoHeaderSize uint32 // bcSize, biSize, etc.
	sizeImage      uint32 // The biSizeImage field; 0 if not available
	compression    uint32 // The biCompression field

	palNumEntries    int
	palBytesPerEntry int
	palSizeInBytes   int

	hasBitfieldsSegment  bool
	bitfieldsSegmentSize int64
	hasProfile           bool
	profileIsLinked      bool
	profileOffset        int64
	profileSize          int64
	isCompressed         bool
	topDown              bool

	// The number of bytes from the start of one row to the start of the next
	// row, and the number of bytes in the whole image (or the number of bytes
	// there would be if the image were not compressed).
	rowStride      int64
	calculatedSize int64

	actualBitsSize int64 // 0 = unknown

	fieldNamePrefix string

	badColorFlag   bool
	badColorWarned bool
	badColorIndex  int
	badColor_X     int
	badColor_Y     int
}

// A wrapper for fmt.Printf.
func (ctx *ctx_type) printf(format string, a ...interface{}) (n int, err error) {
	return fmt.Printf(format, a...)
}

// Print an unformatted string.
func (ctx *ctx_type) print(s string) (n int, err error) {
	return fmt.Print(s)
}

func startLineAbsolute(ctx *ctx_type, pos int64) {
	ctx.printf("%7d: ", pos)
}

func startLine(ctx *ctx_type, offset int64) {
	startLineAbsolute(ctx, ctx.pos+offset)
}

// Start a new line, and print the "bi" (etc.) field name prefix.
func (ctx *ctx_type) pfxPrintf(offset int64, format string, a ...interface{}) {
	startLine(ctx, offset)
	ctx.print(ctx.fieldNamePrefix)
	ctx.printf(format, a...)
}

// DWORD is an unsigned 32-bit little-endian integer.
func getDWORD(d []byte) uint32 {
	return binary.LittleEndian.Uint32(d[0:4])
}

// WORD is an unsigned 16-bit little-endian integer.
func getWORD(d []byte) uint16 {
	return binary.LittleEndian.Uint16(d[0:2])
}

// LONG is a signed 32-bit little-endian integer.
func getLONG(d []byte) int32 {
	return int32(getDWORD(d))
}

func getFloat16dot16(d []byte) float64 {
	return float64(getDWORD(d)) / 65536.0
}

func getFloat2dot30(d []byte) float64 {
	return float64(getDWORD(d)) / 1073741824.0
}

// Functions named "inspect*" are passed a slice to read from,
// and do not modify ctx.pos.
// Functions named "read*" read directly from ctx.data, and
// are responsible for updating ctx.pos.

func inspectFileheader(ctx *ctx_type, d []byte) error {

	startLine(ctx, 0)
	ctx.print("----- FILEHEADER -----\n")

	startLine(ctx, 0)
	ctx.printf("bfType: 0x%02x 0x%02x (%+q)\n", d[0], d[1], d[0:2])
	if d[0] != 0x42 || d[1] != 0x4d {
		return errors.New("Not a BMP file")
	}

	bfSize := getDWORD(d[2:6])
	startLine(ctx, 2)
	ctx.printf("bfSize: %v\n", bfSize)
	if int64(bfSize) != ctx.fileSize {
		ctx.printf("Warning: Reported file size (%v) does not equal actual file size (%v)\n",
			bfSize, ctx.fileSize)
	}

	bfReserved1 := getWORD(d[6:8])
	startLine(ctx, 6)
	ctx.printf("bfReserved1: %v\n", bfReserved1)

	bfReserved2 := getWORD(d[8:10])
	startLine(ctx, 8)
	ctx.printf("bfReserved2: %v\n", bfReserved2)

	ctx.bfOffBits = getDWORD(d[10:14])
	startLine(ctx, 10)
	ctx.printf("bfOffBits: %v\n", ctx.bfOffBits)

	return nil
}

func inspectInfoheaderOS2(ctx *ctx_type, d []byte) error {

	bcWidth := getWORD(d[4:6])
	ctx.pfxPrintf(4, "Width: %v\n", bcWidth)
	ctx.imgWidth = int(bcWidth)

	bcHeight := getWORD(d[6:8])
	ctx.pfxPrintf(6, "Height: %v\n", bcHeight)
	ctx.imgHeight = int(bcHeight)

	bcPlanes := getWORD(d[8:10])
	ctx.pfxPrintf(8, "Planes: %v\n", bcPlanes)

	bcBitCount := getWORD(d[10:12])
	ctx.pfxPrintf(10, "BitCount: %v\n", bcBitCount)
	ctx.bitCount = int(bcBitCount)

	ctx.palBytesPerEntry = 3

	if bcBitCount <= 8 {
		ctx.palNumEntries = 1 << bcBitCount

		bytesAvailableForPalette := int(ctx.bfOffBits) - (14 + int(ctx.infoHeaderSize))
		if bytesAvailableForPalette >= 3 && bytesAvailableForPalette < 3*ctx.palNumEntries {
			ctx.palNumEntries = bytesAvailableForPalette / 3
			ctx.printf("Warning: Bitmap overlaps color table. Assuming there are only %d colors in color table\n",
				ctx.palNumEntries)
		}
	}
	return nil
}

func printDotsPerMeter(ctx *ctx_type, n int32) {
	ctx.printf("%v", n)
	if n != 0 {
		ctx.printf(" (%.2f dpi)", float64(n)*0.0254)
	}
	ctx.print("\n")
}

func inspectInfoheaderV3(ctx *ctx_type, d []byte) error {
	var ok bool

	biWidth := getLONG(d[4:8])
	ctx.pfxPrintf(4, "Width: %v\n", biWidth)
	ctx.imgWidth = int(biWidth)
	if ctx.imgWidth < 1 {
		ctx.print("Warning: Bad width\n")
		ctx.printPixels = false
	}

	biHeight := getLONG(d[8:12])
	ctx.pfxPrintf(8, "Height: %v", biHeight)
	if biHeight < 0 {
		ctx.topDown = true
		ctx.imgHeight = int(-biHeight)
		ctx.printf(" (%v pixels, top-down)", ctx.imgHeight)
	} else {
		ctx.imgHeight = int(biHeight)
	}
	ctx.print("\n")
	if ctx.imgHeight < 1 {
		ctx.print("Warning: Bad height\n")
		ctx.printPixels = false
	}

	biPlanes := getWORD(d[12:14])
	ctx.pfxPrintf(12, "Planes: %v\n", biPlanes)
	if biPlanes != 1 {
		ctx.print("Warning: Planes is required to be 1\n")
	}

	biBitCount := getWORD(d[14:16])
	ctx.pfxPrintf(14, "BitCount: %v\n", biBitCount)
	ctx.bitCount = int(biBitCount)

	ctx.compression = getDWORD(d[16:20])
	ctx.pfxPrintf(16, "Compression: %v", ctx.compression)
	var cmprName string
	cmprName, ok = cmprNames[ctx.compression]
	if !ok {
		cmprName = "(unrecognized)"
	}
	ctx.printf(" = %v\n", cmprName)
	if ctx.compression != bI_RGB && ctx.compression != bI_BITFIELDS &&
		ctx.compression != bI_ALPHABITFIELDS {
		ctx.isCompressed = true
		if ctx.topDown {
			ctx.print("Warning: Compressed images may not be top-down\n")
			ctx.printPixels = false
		}
	}

	if ctx.compression == bI_BITFIELDS && ctx.bmpVerID == "3" {
		ctx.hasBitfieldsSegment = true
		ctx.bitfieldsSegmentSize = 12
	} else if ctx.compression == bI_ALPHABITFIELDS && ctx.bmpVerID == "3" {
		ctx.hasBitfieldsSegment = true
		ctx.bitfieldsSegmentSize = 16
	}

	ctx.sizeImage = getDWORD(d[20:24])
	ctx.pfxPrintf(20, "SizeImage: %v\n", ctx.sizeImage)
	if ctx.sizeImage == 0 && ctx.isCompressed {
		ctx.print("Warning: SizeImage is required for compressed images\n")
	}

	biXPelsPerMeter := getLONG(d[24:28])
	ctx.pfxPrintf(24, "XPelsPerMeter: ")
	printDotsPerMeter(ctx, biXPelsPerMeter)

	biYPelsPerMeter := getLONG(d[28:32])
	ctx.pfxPrintf(28, "YPelsPerMeter: ")
	printDotsPerMeter(ctx, biYPelsPerMeter)

	biClrUsed := getDWORD(d[32:36])
	ctx.pfxPrintf(32, "ClrUsed: %v\n", biClrUsed)

	biClrImportant := getDWORD(d[36:40])
	ctx.pfxPrintf(36, "ClrImportant: %v\n", biClrImportant)

	if biClrUsed > 100000 {
		return errors.New("Unreasonable color table size")
	}

	ctx.palBytesPerEntry = 4

	if biBitCount > 0 && biBitCount <= 8 {
		if biClrUsed == 0 {
			ctx.palNumEntries = 1 << uint(biBitCount)
		} else {
			ctx.palNumEntries = int(biClrUsed)
		}
	} else {
		ctx.palNumEntries = int(biClrUsed)
	}

	return nil
}

func formatCIEXYZ(ctx *ctx_type, d []byte) string {
	x := getFloat2dot30(d[0:4])
	y := getFloat2dot30(d[4:8])
	z := getFloat2dot30(d[8:12])
	return fmt.Sprintf("X:%.8f Y:%.8f Z:%.8f", x, y, z)
}

func inspectCIEXYZTRIPLE(ctx *ctx_type, d []byte, offset int64) {
	ctx.pfxPrintf(offset, "Endpoints: Red:   %s\n", formatCIEXYZ(ctx, d[0:12]))
	ctx.pfxPrintf(offset+12, "Endpoints: Green: %s\n", formatCIEXYZ(ctx, d[12:24]))
	ctx.pfxPrintf(offset+24, "Endpoints: Blue:  %s\n", formatCIEXYZ(ctx, d[24:36]))
}

func csTypeIsValid(ctx *ctx_type, csType uint32) bool {
	if ctx.bmpVerID == "4" {
		if csType == lCS_CALIBRATED_RGB {
			return true
		}
	} else if ctx.bmpVerID == "5" {
		switch csType {
		case lCS_CALIBRATED_RGB, lCS_sRGB, lCS_WINDOWS_COLOR_SPACE,
			pROFILE_LINKED, pROFILE_EMBEDDED:
			return true
		}
	}
	return false
}

func inspectInfoheaderOS2V2(ctx *ctx_type, d []byte) error {
	var err error
	var tmpui16 uint16
	var tmpui32 uint32

	err = inspectInfoheaderV3(ctx, d[0:40])
	if err != nil {
		return err
	}

	units := getWORD(d[40:42])
	ctx.pfxPrintf(40, "Units: %d\n", units)

	tmpui16 = getWORD(d[42:44])
	ctx.pfxPrintf(42, "Reserved: %d\n", tmpui16)
	tmpui16 = getWORD(d[44:46])
	ctx.pfxPrintf(44, "Recording: %d\n", tmpui16)
	tmpui16 = getWORD(d[46:48])
	ctx.pfxPrintf(46, "Rendering: %d\n", tmpui16)

	tmpui32 = getDWORD(d[48:52])
	ctx.pfxPrintf(48, "Size1: %d\n", tmpui32)
	tmpui32 = getDWORD(d[52:56])
	ctx.pfxPrintf(52, "Size2: %d\n", tmpui32)
	tmpui32 = getDWORD(d[56:60])
	ctx.pfxPrintf(56, "ColorEncoding: %d\n", tmpui32)
	tmpui32 = getDWORD(d[60:64])
	ctx.pfxPrintf(60, "Identifier: %d\n", tmpui32)
	return nil
}

func inspectInfoheaderV4(ctx *ctx_type, d []byte) error {
	var err error
	var ok bool
	var name string

	err = inspectInfoheaderV3(ctx, d[0:40])
	if err != nil {
		return err
	}

	redMask := getDWORD(d[40:44])
	ctx.pfxPrintf(40, "RedMask:   %032b\n", redMask)
	greenMask := getDWORD(d[44:48])
	ctx.pfxPrintf(44, "GreenMask: %032b\n", greenMask)
	blueMask := getDWORD(d[48:52])
	ctx.pfxPrintf(48, "BlueMask:  %032b\n", blueMask)
	alphaMask := getDWORD(d[52:56])
	ctx.pfxPrintf(52, "AlphaMask: %032b\n", alphaMask)

	csType := getDWORD(d[56:60])
	ctx.pfxPrintf(56, "CSType: 0x%x", csType)
	name, ok = csTypeNames[csType]
	if ok {
		ctx.printf(" = %s", name)
	}
	if !csTypeIsValid(ctx, csType) {
		ctx.print(" (invalid?)")
	}
	ctx.print("\n")
	if csType == pROFILE_LINKED {
		ctx.hasProfile = true
		ctx.profileIsLinked = true
	} else if csType == pROFILE_EMBEDDED {
		ctx.hasProfile = true
	}

	inspectCIEXYZTRIPLE(ctx, d[60:96], 60)

	gammaRed := getFloat16dot16(d[96:100])
	ctx.pfxPrintf(96, "GammaRed:   %.6f\n", gammaRed)

	gammaGreen := getFloat16dot16(d[100:104])
	ctx.pfxPrintf(100, "GammaGreen: %.6f\n", gammaGreen)

	gammaBlue := getFloat16dot16(d[104:108])
	ctx.pfxPrintf(104, "GammaBlue:  %.6f\n", gammaBlue)

	return nil
}

func inspectInfoheaderV5(ctx *ctx_type, d []byte) error {
	var err error
	var ok bool
	var name string

	err = inspectInfoheaderV4(ctx, d[0:108])
	if err != nil {
		return err
	}

	intent := getDWORD(d[108:112])
	ctx.pfxPrintf(108, "Intent: %v", intent)
	name, ok = intentNames[intent]
	if ok {
		ctx.printf(" = %s", name)
	}
	ctx.print("\n")

	profileData := getDWORD(d[112:116])
	ctx.pfxPrintf(112, "ProfileData: %v\n", profileData)

	profileSize := getDWORD(d[116:120])
	ctx.pfxPrintf(116, "ProfileSize: %v\n", profileSize)

	if ctx.hasProfile {
		ctx.profileOffset = 14 + int64(profileData)
		ctx.profileSize = int64(profileSize)
	}

	reserved := getDWORD(d[120:124])
	ctx.pfxPrintf(120, "Reserved: %v\n", reserved)

	return nil
}

func inspectBitfields(ctx *ctx_type, d []byte) error {
	var colorNames = [4]string{"Red:  ", "Green:", "Blue: ", "Alpha:"}

	startLine(ctx, 0)
	ctx.print("----- BITFIELDS -----\n")

	for i, v := range colorNames {
		if i*4 >= len(d) {
			break
		}
		u := getDWORD(d[i*4 : i*4+4])
		startLine(ctx, int64(i)*4)
		ctx.printf("%s %032b\n", v, u)

	}
	return nil
}

func inspectColorTable(ctx *ctx_type, d []byte) error {
	var i int
	var r, g, b uint8
	var x uint8

	startLine(ctx, 0)
	ctx.print("----- Color table -----\n")
	startLine(ctx, 0)
	ctx.printf("(Number of colors: %v)\n", ctx.palNumEntries)

	if ctx.bmpVerID == "os2V2" {
		bytesAvailableForPalette := int(ctx.bfOffBits) - (14 + int(ctx.infoHeaderSize))
		if bytesAvailableForPalette == 3*ctx.palNumEntries {
			// Some of the (very few) os2V2 sample files I've seen have this
			// problem. It may not be widespread, so this hack may be fairly
			// useless. But it shouldn't hurt anything.
			ctx.printf("Warning: Bitmap overlaps color table. Assuming there are three bytes " +
				"per color table entry, instead of four\n")
			ctx.palBytesPerEntry = 3
			ctx.palSizeInBytes = ctx.palNumEntries * ctx.palBytesPerEntry
		}
	}

	// Print a header line
	if ctx.palBytesPerEntry == 4 {
		startLine(ctx, 0)
		ctx.print("       R  G  B  x\n")
		startLine(ctx, 0)
		ctx.print("       -- -- -- --\n")
	} else {
		startLine(ctx, 0)
		ctx.print("       R  G  B\n")
		startLine(ctx, 0)
		ctx.print("       -- -- --\n")
	}

	for i = 0; i < ctx.palNumEntries; i++ {
		b = d[i*ctx.palBytesPerEntry]
		g = d[i*ctx.palBytesPerEntry+1]
		r = d[i*ctx.palBytesPerEntry+2]
		if ctx.palBytesPerEntry == 4 {
			x = d[i*ctx.palBytesPerEntry+3]
		}

		startLine(ctx, int64(i*ctx.palBytesPerEntry))
		if ctx.bitCount <= 4 {
			ctx.printf("   %x = ", i)
		} else if ctx.bitCount <= 8 {
			ctx.printf("  %02x = ", i)
		} else {
			// Not indexed color, so printing "=" would be misleading.
			ctx.printf("[%3d]  ", i)
		}
		ctx.printf("%02x %02x %02x", r, g, b)
		if ctx.palBytesPerEntry == 4 {
			ctx.printf(" %02x", x)
		}
		ctx.print("\n")
	}
	return nil
}

func checkBitCount(ctx *ctx_type) error {
	var ok bool
	ok = false

	switch ctx.bitCount {
	case 0:
		if (ctx.bmpVerID == "4" || ctx.bmpVerID == "5") &&
			(ctx.compression == bI_JPEG || ctx.compression == bI_PNG) {
			ok = true
		}
	case 1, 4, 8, 24:
		ok = true
	case 2:
		if ctx.bmpVerID == "3" {
			ok = true
		}
	case 16, 32:
		if ctx.bmpVerID == "3" || ctx.bmpVerID == "4" || ctx.bmpVerID == "5" {
			ok = true
		}
	}
	if !ok {
		return errors.New("Invalid BitCount")
	}
	return nil
}

func readInfoheader(ctx *ctx_type) error {
	var err error

	if ctx.fileSize-ctx.pos < 4 {
		return errors.New("Unexpected end of file")
	}

	startLine(ctx, 0)
	ctx.print("----- INFOHEADER -----\n")

	// Read the "biSize" field, which tells us the BMP version.
	ctx.infoHeaderSize = getDWORD(ctx.data[ctx.pos : ctx.pos+4])
	startLine(ctx, 0)
	ctx.printf("Info Header Size: %v", ctx.infoHeaderSize)

	var vi versionInfo_type
	var knownVersion bool
	vi, knownVersion = versionInfo[ctx.infoHeaderSize]
	if knownVersion {
		ctx.printf(" (%s)", vi.name)
	}
	ctx.print("\n")

	if ctx.fileSize-ctx.pos < int64(ctx.infoHeaderSize) {
		return errors.New("Unexpected end of file")
	}

	if !knownVersion {
		return errors.New("Unknown BMP version")
	}
	if vi.inspectInfoheaderFunc == nil {
		return errors.New("Unsupported BMP version")
	}

	ctx.bmpVerID = vi.id
	ctx.fieldNamePrefix = vi.prefix

	err = vi.inspectInfoheaderFunc(ctx, ctx.data[ctx.pos:ctx.pos+int64(ctx.infoHeaderSize)])
	if err != nil {
		return err
	}

	err = checkBitCount(ctx)
	if err != nil {
		return err
	}

	ctx.palSizeInBytes = ctx.palBytesPerEntry * ctx.palNumEntries

	ctx.pos += int64(ctx.infoHeaderSize)

	return nil
}

// Record information about a bad palette index.
func badColor(ctx *ctx_type, n int, xpos int) {
	if !ctx.badColorFlag {
		ctx.badColorIndex = n
		ctx.badColor_X = xpos
		ctx.badColorFlag = true
	}
}

func printRow_1(ctx *ctx_type, d []byte) {
	var i int
	var n byte
	ctx.print(" ")
	for i = 0; i < ctx.imgWidth; i++ {
		n = d[i/8]
		n = n & (1 << (7 - uint(i)%8))
		if n == 0 {
			ctx.print("0")
		} else {
			n = 1
			ctx.print("1")
		}
		if int(n) >= ctx.palNumEntries {
			badColor(ctx, int(n), i)
		}
	}
}

func printRow_2(ctx *ctx_type, d []byte) {
	var i int
	var n byte

	ctx.print(" ")
	for i = 0; i < ctx.imgWidth; i++ {
		n = (d[i/4] >> (2 * (3 - uint(i)%4))) & 0x03
		ctx.printf("%x", n)
		if int(n) >= ctx.palNumEntries {
			badColor(ctx, int(n), i)
		}
	}
}

func printRow_4(ctx *ctx_type, d []byte) {
	var i int
	var n byte

	ctx.print(" ")
	for i = 0; i < ctx.imgWidth; i++ {
		n = d[i/2]
		if i%2 == 0 {
			n = n >> 4
		} else {
			n = n & 0x0f
		}
		ctx.printf("%x", n)
		if int(n) >= ctx.palNumEntries {
			badColor(ctx, int(n), i)
		}
	}
}

func printRow_8(ctx *ctx_type, d []byte) {
	var i int
	var n byte

	for i = 0; i < ctx.imgWidth; i++ {
		n = d[i]
		ctx.printf(" %02x", n)
		if int(n) >= ctx.palNumEntries {
			badColor(ctx, int(n), i)
		}
	}
}

func printRow_16(ctx *ctx_type, d []byte) {
	var i int
	for i = 0; i < ctx.imgWidth; i++ {
		ctx.printf(" %04x", getWORD(d[i*2:i*2+2]))
	}
}

func printRow_24(ctx *ctx_type, d []byte) {
	var i int
	var r, b, g byte
	for i = 0; i < ctx.imgWidth; i++ {
		b = d[i*3]
		g = d[i*3+1]
		r = d[i*3+2]
		ctx.printf(" %02x%02x%02x", r, g, b)
	}
}

func printRow_32(ctx *ctx_type, d []byte) {
	var i int
	for i = 0; i < ctx.imgWidth; i++ {
		ctx.printf(" %08x", getDWORD(d[i*4:i*4+4]))
	}
}

type printRowFuncType func(ctx *ctx_type, d []byte)

var printRowFuncs = map[int]printRowFuncType{
	1:  printRow_1,
	2:  printRow_2,
	4:  printRow_4,
	8:  printRow_8,
	16: printRow_16,
	24: printRow_24,
	32: printRow_32,
}

func printUncompressedPixels(ctx *ctx_type, d []byte) {
	var rowPhysical int64
	var rowLogical int64
	var offset int64
	var pR printRowFuncType

	// Select a low-level "print row" function.
	pR = printRowFuncs[ctx.bitCount]
	if pR == nil {
		return
	}

	for rowPhysical = 0; rowPhysical < int64(ctx.imgHeight); rowPhysical++ {
		if ctx.topDown {
			rowLogical = rowPhysical
		} else {
			rowLogical = int64(ctx.imgHeight) - 1 - rowPhysical
		}

		offset = rowPhysical * ctx.rowStride
		startLine(ctx, offset)
		ctx.printf("row %d:", rowLogical)
		pR(ctx, d[offset:offset+ctx.rowStride])
		ctx.print("\n")

		// At the end of the row, display any pending warning.
		if ctx.badColorFlag && !ctx.badColorWarned {
			ctx.printf("Warning: Bad palette index 0x%02x at (%d,%d)\n", ctx.badColorIndex,
				ctx.badColor_X, rowLogical)
			ctx.badColorWarned = true
		}
	}
}

type rlectx_type struct {
	bytesInThisRow   int
	rowHeaderPrinted bool
	xpos, ypos       int

	badPosFlag   bool
	badPosWarned bool
	badPos_X     int
	badPos_Y     int
}

// Do some things that need to be done at the end of every row.
func endRLERow(ctx *ctx_type, rlectx *rlectx_type) {
	if rlectx.rowHeaderPrinted {
		ctx.printf(" [%v bytes]\n", rlectx.bytesInThisRow)
		rlectx.bytesInThisRow = 0
		rlectx.rowHeaderPrinted = false
	}

	// Print pending warnings.

	if rlectx.badPosFlag && !rlectx.badPosWarned {
		ctx.printf("Warning: Out of bounds pixel (%d,%d)\n", rlectx.badPos_X, rlectx.badPos_Y)
		rlectx.badPosWarned = true
	}

	if ctx.badColorFlag && !ctx.badColorWarned {
		ctx.printf("Warning: Bad palette index 0x%02x at (%d,%d)\n", ctx.badColorIndex,
			ctx.badColor_X, ctx.badColor_Y)
		ctx.badColorWarned = true
	}
}

func checkRLEPosAndColor(ctx *ctx_type, rlectx *rlectx_type, n byte) {
	if (rlectx.xpos >= ctx.imgWidth || rlectx.ypos < 0) && !rlectx.badPosFlag {
		rlectx.badPosFlag = true
		rlectx.badPos_X = rlectx.xpos
		rlectx.badPos_Y = rlectx.ypos
	}

	if int(n) >= ctx.palNumEntries && !ctx.badColorFlag {
		ctx.badColorIndex = int(n)
		ctx.badColor_X = rlectx.xpos
		ctx.badColor_Y = rlectx.ypos
		ctx.badColorFlag = true
	}
}

func printRLE4Pixel(ctx *ctx_type, rlectx *rlectx_type, n byte) {
	ctx.printf("%x", n)
	checkRLEPosAndColor(ctx, rlectx, n)
}

func printRLE8Pixel(ctx *ctx_type, rlectx *rlectx_type, n byte) {
	ctx.printf("%02x", n)
	checkRLEPosAndColor(ctx, rlectx, n)
}

func printRLECompressedPixels(ctx *ctx_type, d []byte) {
	if ctx.bitCount != 4 && ctx.bitCount != 8 {
		return
	}
	if ctx.bitCount == 4 && ctx.compression != bI_RLE4 {
		return
	}
	if ctx.bitCount == 8 && ctx.compression != bI_RLE8 {
		return
	}

	rlectx := new(rlectx_type)
	var pos int = 0 // current position in d[]
	var unc_pixels_left int = 0
	var b1, b2 byte
	var deltaFlag bool

	rlectx.xpos = 0
	// RLE-compressed BMPs are not allowed to be top-down.
	rlectx.ypos = ctx.imgHeight - 1

	for {
		if pos+1 >= len(d) {
			// Compressed data ended without an EOBMP code.
			endRLERow(ctx, rlectx)
			break
		}

		if !rlectx.rowHeaderPrinted {
			startLine(ctx, int64(pos))
			if rlectx.ypos >= 0 {
				ctx.printf("row %d:", rlectx.ypos)
			} else {
				ctx.print("row n/a:")
			}
			rlectx.rowHeaderPrinted = true
		}

		// Read bytes 2 at a time
		b1 = d[pos]
		b2 = d[pos+1]
		pos += 2
		rlectx.bytesInThisRow += 2

		if unc_pixels_left > 0 {
			if ctx.compression == bI_RLE4 {
				// The two bytes we read store up to 4 uncompressed pixels.
				printRLE4Pixel(ctx, rlectx, b1>>4)
				rlectx.xpos++
				unc_pixels_left--
				if unc_pixels_left > 0 {
					printRLE4Pixel(ctx, rlectx, b1&0x0f)
					rlectx.xpos++
					unc_pixels_left--
				}
				if unc_pixels_left > 0 {
					printRLE4Pixel(ctx, rlectx, b2>>4)
					rlectx.xpos++
					unc_pixels_left--
				}
				if unc_pixels_left > 0 {
					printRLE4Pixel(ctx, rlectx, b2&0x0f)
					rlectx.xpos++
					unc_pixels_left--
				}
			} else { // RLE8
				printRLE8Pixel(ctx, rlectx, b1)
				rlectx.xpos++
				unc_pixels_left--
				if unc_pixels_left > 0 {
					ctx.print(" ")
					printRLE8Pixel(ctx, rlectx, b2)
					rlectx.xpos++
					unc_pixels_left--
				}
				if unc_pixels_left > 0 {
					ctx.print(" ")
				}
			}
			if unc_pixels_left == 0 {
				ctx.print("}")
			}
		} else if deltaFlag {
			ctx.printf("(%v,%v)", b1, b2)
			rlectx.xpos += int(b1)
			rlectx.ypos -= int(b2)
			if b2 > 0 {
				// A nonzero y delta moves us to a different row, so end the
				// current row.
				endRLERow(ctx, rlectx)
			}
			deltaFlag = false
		} else if b1 == 0 {
			if b2 == 0 {
				ctx.print(" EOL")
				endRLERow(ctx, rlectx)
				rlectx.ypos--
				rlectx.xpos = 0
			} else if b2 == 1 {
				ctx.print(" EOBMP")
				endRLERow(ctx, rlectx)
				break
			} else if b2 == 2 {
				ctx.print(" DELTA")
				deltaFlag = true
			} else {
				// An upcoming uncompressed run of b2 pixels
				ctx.printf(" u%v{", b2)
				unc_pixels_left = int(b2)
			}
		} else { // Compressed pixels
			if ctx.compression == bI_RLE4 {
				var n1 byte = (b2 & 0xf0) >> 4
				var n2 byte = b2 & 0x0f
				if b1 == 1 {
					ctx.printf(" %v{%x}", b1, n1)
				} else if n1 == n2 {
					ctx.printf(" %v{%x}", b1, n1)
				} else {
					ctx.printf(" %v{%x%x}", b1, n1, n2)
				}

				// Check the first pixel of this run for valid color and position.
				checkRLEPosAndColor(ctx, rlectx, n1)
				rlectx.xpos++
				if b1 > 1 {
					// Check the second pixel.
					checkRLEPosAndColor(ctx, rlectx, n2)
					rlectx.xpos++

					if b1 > 2 {
						// Check the last pixel's position.
						rlectx.xpos += int(b1) - 3
						checkRLEPosAndColor(ctx, rlectx, 0)
						rlectx.xpos++
					}
				}

			} else { // RLE8
				ctx.printf(" %v{%02x}", b1, b2)

				// Check the first and last pixel of this run.
				checkRLEPosAndColor(ctx, rlectx, b2)
				rlectx.xpos += int(b1) - 1
				checkRLEPosAndColor(ctx, rlectx, b2)
				rlectx.xpos++
			}
		}
	}

	ctx.actualBitsSize = int64(pos)
	startLine(ctx, int64(pos))
	var ratio float64
	ratio = float64(ctx.actualBitsSize) / float64(ctx.calculatedSize)
	ctx.printf("(Compression ratio: %v/%v = %.2f%%)\n", ctx.actualBitsSize,
		ctx.calculatedSize, ratio*100.0)
}

func inspectBits(ctx *ctx_type, d []byte) error {
	startLine(ctx, 0)
	ctx.print("----- Bitmap bits -----\n")
	startLine(ctx, 0)
	ctx.print("(Size given by SizeImage field:     ")
	if ctx.sizeImage == 0 {
		ctx.print("n/a)\n")
	} else {
		ctx.printf("%v)\n", ctx.sizeImage)
	}

	ctx.rowStride = (((int64(ctx.imgWidth) * int64(ctx.bitCount)) + 31) / 32) * 4
	ctx.calculatedSize = ctx.rowStride * int64(ctx.imgHeight)
	startLine(ctx, 0)
	ctx.print("(Size calculated from width/height: ")
	if ctx.isCompressed {
		// Can't predict the size of compressed images.
		ctx.print("n/a)\n")
	} else {
		// An uncompressed image
		ctx.actualBitsSize = ctx.calculatedSize
		ctx.printf("%v)\n", ctx.calculatedSize)
	}

	startLine(ctx, 0)
	ctx.print("(Size implied by file size:         ")
	if ctx.hasProfile {
		ctx.print("n/a")
	} else {
		ctx.printf("%v", len(d))
	}
	ctx.print(")\n")

	if !ctx.isCompressed {
		if ctx.rowStride < 1 || ctx.rowStride > 1000000 {
			ctx.printPixels = false
		} else if int64(len(d)) < ctx.calculatedSize {
			ctx.print("Warning: Unexpected end of file\n")
			ctx.printPixels = false
		}
	}

	if ctx.printPixels {
		if ctx.isCompressed {
			if ctx.compression == bI_RLE4 || ctx.compression == bI_RLE8 {
				printRLECompressedPixels(ctx, d)
			}
		} else {
			printUncompressedPixels(ctx, d)
		}
	}

	return nil
}

func inspectProfile(ctx *ctx_type, d []byte) {
	startLine(ctx, 0)
	ctx.print("----- Color profile -----\n")
	startLine(ctx, 0)
	ctx.printf("(Profile size: %v)\n", len(d))
}

func printWindows1252String(ctx *ctx_type, d []byte) {
	for i := 0; i < len(d); i++ {
		if d[i] == '\\' || d[i] == '"' {
			// Printable ASCII characters that need escaped
			ctx.printf("\\")
		}
		if d[i] >= 32 && d[i] <= 126 {
			// Printable ASCII characters
			ctx.printf("%c", d[i])
		} else {
			// Non-ASCII or non-printable characters
			ctx.printf("\\x%02x", d[i])
		}
	}
}

// Display the filename of the linked profile.
// The filename is supposed to be NUL-terminated, and use the Windows-1252
// character set.
func inspectLinkedProfile(ctx *ctx_type, d []byte) {
	startLine(ctx, 0)
	ctx.print("----- Linked color profile -----\n")
	startLine(ctx, 0)
	ctx.print("Filename: \"")
	printWindows1252String(ctx, d)
	ctx.print("\"\n")
}

func readBmp(ctx *ctx_type) error {
	var err error

	if ctx.fileSize-ctx.pos < 14 {
		return errors.New("File is too small to be a BMP")
	}

	err = inspectFileheader(ctx, ctx.data[ctx.pos:ctx.pos+14])
	if err != nil {
		return err
	}
	ctx.pos += 14

	err = readInfoheader(ctx)
	if err != nil {
		return err
	}

	if ctx.hasBitfieldsSegment {
		if ctx.fileSize-ctx.pos < ctx.bitfieldsSegmentSize {
			return errors.New("Unexpected end of file")
		}
		err = inspectBitfields(ctx, ctx.data[ctx.pos:ctx.pos+ctx.bitfieldsSegmentSize])
		if err != nil {
			return err
		}
		ctx.pos += ctx.bitfieldsSegmentSize
	}

	if ctx.palSizeInBytes > 0 {
		if ctx.fileSize-ctx.pos < int64(ctx.palSizeInBytes) {
			return errors.New("Unexpected end of file")
		}
		err = inspectColorTable(ctx, ctx.data[ctx.pos:ctx.pos+int64(ctx.palSizeInBytes)])
		if err != nil {
			return err
		}
		ctx.pos += int64(ctx.palSizeInBytes)
	}

	// Is the bfOffBits pointer sensible?
	if int64(ctx.bfOffBits) < ctx.pos || int64(ctx.bfOffBits) > ctx.fileSize {
		return errors.New("Bad bfOffBits value")
	}

	var unusedBytes int64
	unusedBytes = int64(ctx.bfOffBits) - ctx.pos
	if unusedBytes > 0 {
		startLineAbsolute(ctx, ctx.pos)
		ctx.printf("----- %v unused bytes -----\n", unusedBytes)
	}
	ctx.pos += unusedBytes

	// Assume the rest of the file contains the bitmap bits
	err = inspectBits(ctx, ctx.data[ctx.pos:ctx.fileSize])
	if err != nil {
		return err
	}

	if ctx.actualBitsSize < 1 {
		return nil
	}

	ctx.pos += ctx.actualBitsSize

	if ctx.hasProfile {
		if ctx.pos < ctx.profileOffset {
			startLineAbsolute(ctx, ctx.pos)
			ctx.printf("----- %v unused bytes -----\n", ctx.profileOffset-ctx.pos)
			ctx.pos = ctx.profileOffset
		} else if ctx.pos > ctx.profileOffset {
			return errors.New("Invalid color profile location")
		}

		if ctx.pos+ctx.profileSize > ctx.fileSize {
			return errors.New("Invalid color profile size")
		}

		if ctx.profileIsLinked {
			inspectLinkedProfile(ctx, ctx.data[ctx.pos:ctx.pos+ctx.profileSize])
		} else {
			inspectProfile(ctx, ctx.data[ctx.pos:ctx.pos+ctx.profileSize])
		}
		ctx.pos += ctx.profileSize
	}

	if ctx.pos < ctx.fileSize {
		startLineAbsolute(ctx, ctx.pos)
		ctx.printf("----- %v unused bytes -----\n", ctx.fileSize-ctx.pos)
	}

	return nil
}

func main2(ctx *ctx_type) error {
	var err error

	if len(os.Args) < 2 {
		return errors.New("Usage error")
	}
	ctx.fileName = os.Args[1]

	ctx.printPixels = true

	// Read the whole file into a slice of bytes.
	// TODO: It would be better to read the file in a streaming manner.
	// (Though if we allowed pipes, we'd lose a small amount of functionality
	// that relies on knowing the file size.)
	ctx.data, err = ioutil.ReadFile(ctx.fileName)
	if err != nil {
		return err
	}

	ctx.fileSize = int64(len(ctx.data))

	err = readBmp(ctx)

	startLineAbsolute(ctx, ctx.fileSize)
	ctx.print("----- End of file -----\n")
	return err
}

func main() {
	ctx := new(ctx_type)

	err := main2(ctx)
	if err != nil {
		ctx.printf("Error: %v\n", err.Error())
	}
}
