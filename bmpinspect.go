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
)

var cmprNames = map[uint32]string{
	bI_RGB:       "BI_RGB",
	bI_RLE8:      "BI_RLE8",
	bI_RLE4:      "BI_RLE4",
	bI_BITFIELDS: "BI_BITFIELDS",
	bI_JPEG:      "BI_JPEG",
	bI_PNG:       "BI_PNG",
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

type ctx_type struct {
	fileName string
	data     []byte
	fileSize int64
	pos      int64

	printPixels bool

	bmpVer   int // 2, 3, 4, or 5. 0=unknown
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

	hasBitfieldsSegment bool
	isCompressed        bool
	topDown             bool

	rowStride int64
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

func getDWORD(ctx *ctx_type, d []byte) uint32 {
	return binary.LittleEndian.Uint32(d[0:4])
}

func getWORD(ctx *ctx_type, d []byte) uint16 {
	return binary.LittleEndian.Uint16(d[0:2])
}

func getLONG(ctx *ctx_type, d []byte) int32 {
	// TODO: There's gotta be a better way.
	// Could use encoding/binary.Read(), but that's inconvenient because
	// we're not using an io.Reader.
	var tmp int64
	tmp = int64(getDWORD(ctx, d))
	if tmp > 0x7fffffff {
		tmp -= 0x100000000
	}
	return int32(tmp)
}

func getFloat16dot16(ctx *ctx_type, d []byte) float64 {
	return float64(getDWORD(ctx, d)) / 65536.0
}

func getFloat2dot30(ctx *ctx_type, d []byte) float64 {
	return float64(getDWORD(ctx, d)) / 1073741824.0
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

	bfSize := getDWORD(ctx, d[2:6])
	startLine(ctx, 2)
	ctx.printf("bfSize: %v\n", bfSize)
	if int64(bfSize) != ctx.fileSize {
		ctx.printf("Warning: Reported file size (%v) does not equal actual file size (%v)\n",
			bfSize, ctx.fileSize)
	}

	bfReserved1 := getWORD(ctx, d[6:8])
	startLine(ctx, 6)
	ctx.printf("bfReserved1: %v\n", bfReserved1)

	bfReserved2 := getWORD(ctx, d[8:10])
	startLine(ctx, 8)
	ctx.printf("bfReserved2: %v\n", bfReserved2)

	ctx.bfOffBits = getDWORD(ctx, d[10:14])
	startLine(ctx, 10)
	ctx.printf("bfOffBits: %v\n", ctx.bfOffBits)

	return nil
}

func inspectInfoheaderV2(ctx *ctx_type, d []byte) error {

	bcWidth := getWORD(ctx, d[4:6])
	startLine(ctx, 4)
	ctx.printf("bcWidth: %v\n", bcWidth)
	ctx.imgWidth = int(bcWidth)

	bcHeight := getWORD(ctx, d[6:8])
	startLine(ctx, 6)
	ctx.printf("bcHeight: %v\n", bcHeight)
	ctx.imgHeight = int(bcHeight)

	bcPlanes := getWORD(ctx, d[8:10])
	startLine(ctx, 8)
	ctx.printf("bcPlanes: %v\n", bcPlanes)

	bcBitCount := getWORD(ctx, d[10:12])
	startLine(ctx, 10)
	ctx.printf("bcBitCount: %v\n", bcBitCount)
	ctx.bitCount = int(bcBitCount)

	ctx.palBytesPerEntry = 3

	if bcBitCount <= 8 {
		ctx.palNumEntries = 1 << bcBitCount
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

	var prefix string
	var ok bool

	switch ctx.bmpVer {
	case 3:
		prefix = "bi"
	case 4:
		prefix = "bV4"
	case 5:
		prefix = "bV5"
	}

	biWidth := getLONG(ctx, d[4:8])
	startLine(ctx, 4)
	ctx.printf("%vWidth: %v\n", prefix, biWidth)
	ctx.imgWidth = int(biWidth)

	biHeight := getLONG(ctx, d[8:12])
	startLine(ctx, 8)
	ctx.printf("%vHeight: %v\n", prefix, biHeight)
	if biHeight < 0 {
		ctx.topDown = true
		ctx.imgHeight = int(-biHeight)
	} else {
		ctx.imgHeight = int(biHeight)
	}

	biPlanes := getWORD(ctx, d[12:14])
	startLine(ctx, 12)
	ctx.printf("%vPlanes: %v\n", prefix, biPlanes)
	if biPlanes != 1 {
		ctx.print("Warning: Planes is required to be 1\n")
	}

	biBitCount := getWORD(ctx, d[14:16])
	startLine(ctx, 14)
	ctx.printf("%vBitCount: %v\n", prefix, biBitCount)
	ctx.bitCount = int(biBitCount)

	ctx.compression = getDWORD(ctx, d[16:20])
	startLine(ctx, 16)
	ctx.printf("%vCompression: %v", prefix, ctx.compression)
	var cmprName string
	cmprName, ok = cmprNames[ctx.compression]
	if !ok {
		cmprName = "(unrecognized)"
	}
	ctx.printf(" = %v\n", cmprName)
	if ctx.compression != bI_RGB && ctx.compression != bI_BITFIELDS {
		ctx.isCompressed = true
	}

	if ctx.compression == bI_BITFIELDS && ctx.bmpVer == 3 {
		ctx.hasBitfieldsSegment = true
	}

	ctx.sizeImage = getDWORD(ctx, d[20:24])
	startLine(ctx, 20)
	ctx.printf("%vSizeImage: %v\n", prefix, ctx.sizeImage)
	if ctx.sizeImage == 0 && ctx.isCompressed {
		ctx.print("Warning: SizeImage is required for compressed images\n")
	}

	biXPelsPerMeter := getLONG(ctx, d[24:28])
	startLine(ctx, 24)
	ctx.printf("%vXPelsPerMeter: ", prefix)
	printDotsPerMeter(ctx, biXPelsPerMeter)

	biYPelsPerMeter := getLONG(ctx, d[28:32])
	startLine(ctx, 28)
	ctx.printf("%vYPelsPerMeter: ", prefix)
	printDotsPerMeter(ctx, biYPelsPerMeter)

	biClrUsed := getDWORD(ctx, d[32:36])
	startLine(ctx, 32)
	ctx.printf("%vClrUsed: %v\n", prefix, biClrUsed)

	biClrImportant := getDWORD(ctx, d[36:40])
	startLine(ctx, 36)
	ctx.printf("%vClrImportant: %v\n", prefix, biClrImportant)

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
	x := getFloat2dot30(ctx, d[0:4])
	y := getFloat2dot30(ctx, d[4:8])
	z := getFloat2dot30(ctx, d[8:12])
	return fmt.Sprintf("X:%.8f Y:%.8f Z:%.8f", x, y, z)
}

func inspectCIEXYZTRIPLE(ctx *ctx_type, d []byte, offset int64, prefix string) {
	startLine(ctx, offset)
	ctx.printf("%sEndpoints: Red:   %s\n", prefix, formatCIEXYZ(ctx, d[0:12]))

	startLine(ctx, offset+12)
	ctx.printf("%sEndpoints: Green: %s\n", prefix, formatCIEXYZ(ctx, d[12:24]))

	startLine(ctx, offset+24)
	ctx.printf("%sEndpoints: Blue:  %s\n", prefix, formatCIEXYZ(ctx, d[24:36]))
}

func csTypeIsValid(ctx *ctx_type, csType uint32) bool {
	if ctx.bmpVer == 4 {
		if csType == lCS_CALIBRATED_RGB {
			return true
		}
	} else if ctx.bmpVer == 5 {
		switch csType {
		case lCS_CALIBRATED_RGB, lCS_sRGB, lCS_WINDOWS_COLOR_SPACE,
			pROFILE_LINKED, pROFILE_EMBEDDED:
			return true
		}
	}
	return false
}

func inspectInfoheaderV4(ctx *ctx_type, d []byte) error {
	var err error
	var prefix string
	var ok bool
	var name string

	switch ctx.bmpVer {
	case 4:
		prefix = "bV4"
	case 5:
		prefix = "bV5"
	}

	err = inspectInfoheaderV3(ctx, d[0:40])
	if err != nil {
		return err
	}

	redMask := getDWORD(ctx, d[40:44])
	startLine(ctx, 40)
	ctx.printf("%vRedMask:   %032b\n", prefix, redMask)
	greenMask := getDWORD(ctx, d[44:48])
	startLine(ctx, 44)
	ctx.printf("%vGreenMask: %032b\n", prefix, greenMask)
	blueMask := getDWORD(ctx, d[48:52])
	startLine(ctx, 48)
	ctx.printf("%vBlueMask:  %032b\n", prefix, blueMask)
	alphaMask := getDWORD(ctx, d[52:56])
	startLine(ctx, 52)
	ctx.printf("%vAlphaMask: %032b\n", prefix, alphaMask)

	csType := getDWORD(ctx, d[56:60])
	startLine(ctx, 52)
	ctx.printf("%vCSType: 0x%x", prefix, csType)
	name, ok = csTypeNames[csType]
	if ok {
		ctx.printf(" = %s", name)
	}
	if !csTypeIsValid(ctx, csType) {
		ctx.print(" (invalid?)")
	}
	ctx.print("\n")

	inspectCIEXYZTRIPLE(ctx, d[60:96], 60, prefix)

	gammaRed := getFloat16dot16(ctx, d[96:100])
	startLine(ctx, 96)
	ctx.printf("%vGammaRed:   %.6f\n", prefix, gammaRed)

	gammaGreen := getFloat16dot16(ctx, d[100:104])
	startLine(ctx, 100)
	ctx.printf("%vGammaGreen: %.6f\n", prefix, gammaGreen)

	gammaBlue := getFloat16dot16(ctx, d[104:108])
	startLine(ctx, 104)
	ctx.printf("%vGammaBlue:  %.6f\n", prefix, gammaBlue)

	return nil
}

func inspectInfoheaderV5(ctx *ctx_type, d []byte) error {
	var err error
	var prefix string
	var ok bool
	var name string

	prefix = "bV5"

	err = inspectInfoheaderV4(ctx, d[0:108])
	if err != nil {
		return err
	}

	intent := getDWORD(ctx, d[108:112])
	startLine(ctx, 108)
	ctx.printf("%vIntent: %v", prefix, intent)
	name, ok = intentNames[intent]
	if ok {
		ctx.printf(" = %s", name)
	}
	ctx.print("\n")

	profileData := getDWORD(ctx, d[112:116])
	startLine(ctx, 112)
	ctx.printf("%vProfileData: %v\n", prefix, profileData)

	profileSize := getDWORD(ctx, d[116:120])
	startLine(ctx, 116)
	ctx.printf("%vProfileSize: %v\n", prefix, profileSize)

	reserved := getDWORD(ctx, d[120:124])
	startLine(ctx, 120)
	ctx.printf("%vReserved: %v\n", prefix, reserved)

	return nil
}

func inspectBitfields(ctx *ctx_type, d []byte) error {
	var i int64
	var colorNames [3]string

	colorNames = [3]string{"Red:  ", "Green:", "Blue: "}

	startLine(ctx, 0)
	ctx.print("----- BITFIELDS -----\n")

	for i = 0; i < 3; i++ {
		u := getDWORD(ctx, d[i*4:i*4+4])
		startLine(ctx, i*4)
		ctx.printf("%s %032b\n", colorNames[i], u)

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
		if ctx.bmpVer >= 4 && (ctx.compression == bI_JPEG || ctx.compression == bI_PNG) {
			ok = true
		}
	case 1, 4, 8, 24:
		ok = true
	case 16, 32:
		if ctx.bmpVer >= 3 {
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
	ctx.infoHeaderSize = getDWORD(ctx, ctx.data[ctx.pos:ctx.pos+4])
	startLine(ctx, 0)
	ctx.printf("(bc|bi|bV4|bV5)Size: %v\n", ctx.infoHeaderSize)

	if ctx.fileSize-ctx.pos < int64(ctx.infoHeaderSize) {
		return errors.New("Unexpected end of file")
	}

	switch ctx.infoHeaderSize {
	case 12:
		ctx.bmpVer = 2
	case 40:
		ctx.bmpVer = 3
	case 108:
		ctx.bmpVer = 4
	case 124:
		ctx.bmpVer = 5
	}

	switch ctx.bmpVer {
	case 2:
		err = inspectInfoheaderV2(ctx, ctx.data[ctx.pos:ctx.pos+int64(ctx.infoHeaderSize)])
		if err != nil {
			return err
		}
	case 3:
		err = inspectInfoheaderV3(ctx, ctx.data[ctx.pos:ctx.pos+int64(ctx.infoHeaderSize)])
		if err != nil {
			return err
		}
	case 4:
		err = inspectInfoheaderV4(ctx, ctx.data[ctx.pos:ctx.pos+int64(ctx.infoHeaderSize)])
		if err != nil {
			return err
		}
	case 5:
		err = inspectInfoheaderV5(ctx, ctx.data[ctx.pos:ctx.pos+int64(ctx.infoHeaderSize)])
		if err != nil {
			return err
		}
	default:
		return errors.New("Unsupported BMP version")
	}

	err = checkBitCount(ctx)
	if err != nil {
		return err
	}

	ctx.palSizeInBytes = ctx.palBytesPerEntry * ctx.palNumEntries

	ctx.pos += int64(ctx.infoHeaderSize)

	return nil
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
			ctx.print("1")
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
	}
}

func printRow_8(ctx *ctx_type, d []byte) {
	var i int
	for i = 0; i < ctx.imgWidth; i++ {
		ctx.printf(" %02x", d[i])
	}
}

func printRow_16(ctx *ctx_type, d []byte) {
	var i int
	for i = 0; i < ctx.imgWidth; i++ {
		ctx.printf(" %04x", getWORD(ctx, d[i*2:i*2+2]))
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
		ctx.printf(" %08x", getDWORD(ctx, d[i*4:i*4+4]))
	}
}

func printRow(ctx *ctx_type, rowPhysical int64, rowLogical int64, d []byte) {
	var offset int64

	offset = rowPhysical * ctx.rowStride
	startLine(ctx, offset)
	ctx.printf("row %d:", rowLogical)

	switch ctx.bitCount {
	case 1:
		printRow_1(ctx, d[offset:offset+ctx.rowStride])
	case 4:
		printRow_4(ctx, d[offset:offset+ctx.rowStride])
	case 8:
		printRow_8(ctx, d[offset:offset+ctx.rowStride])
	case 16:
		printRow_16(ctx, d[offset:offset+ctx.rowStride])
	case 24:
		printRow_24(ctx, d[offset:offset+ctx.rowStride])
	case 32:
		printRow_32(ctx, d[offset:offset+ctx.rowStride])
	}

	ctx.print("\n")
}

func inspectBits(ctx *ctx_type, d []byte) error {
	var calculatedSize int64

	startLine(ctx, 0)
	ctx.print("----- Bitmap bits -----\n")
	startLine(ctx, 0)
	ctx.print("(Size given by SizeImage field:     ")
	if ctx.sizeImage == 0 {
		ctx.print("n/a)\n")
	} else {
		ctx.printf("%v)\n", ctx.sizeImage)
	}

	startLine(ctx, 0)
	ctx.print("(Size calculated from width/height: ")
	if ctx.isCompressed {
		// Can't predict the size of compressed images.
		ctx.print("n/a)\n")
	} else {
		// An uncompressed image
		ctx.rowStride = (((int64(ctx.imgWidth) * int64(ctx.bitCount)) + 31) / 32) * 4
		calculatedSize = ctx.rowStride * int64(ctx.imgHeight)
		ctx.printf("%v)\n", calculatedSize)
	}

	startLine(ctx, 0)
	ctx.printf("(Size implied by file size:         %v)\n", len(d))

	var printPixels bool
	printPixels = true

	if !ctx.printPixels {
		printPixels = false
	} else if ctx.isCompressed {
		// TODO: Support RLE-compressed images
		printPixels = false
	} else if ctx.rowStride < 1 || ctx.rowStride > 1000000 {
		printPixels = false
	} else if int64(len(d)) < calculatedSize {
		printPixels = false
	}

	if printPixels {
		var rowPhysical int64
		var rowLogical int64
		for rowPhysical = 0; rowPhysical < int64(ctx.imgHeight); rowPhysical++ {
			if ctx.topDown {
				rowLogical = rowPhysical
			} else {
				rowLogical = int64(ctx.imgHeight) - 1 - rowPhysical
			}

			printRow(ctx, rowPhysical, rowLogical, d)
		}

	}

	return nil
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
		if ctx.fileSize-ctx.pos < 12 {
			return errors.New("Unexpected end of file")
		}
		err = inspectBitfields(ctx, ctx.data[ctx.pos:ctx.pos+12])
		if err != nil {
			return err
		}
		ctx.pos += 12
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
