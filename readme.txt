bmpinspect: A simple command-line utility to display the contents of a
Windows BMP image file.

Usage:

  bmpinspect <bmp-file.bmp>

bmpinspect is written in Go; see <http://golang.org/>. It should work on any
system for which a Go compiler is available.

To compile, try running "go build bmpinspect.go".

Copyright (c) 2012 Jason Summers

-------------------------------------------------------------------------
Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
-------------------------------------------------------------------------

Notes:

General
-------

For more information about BMP files, search the web for the following terms:
BITMAPFILEHEADER, BITMAPCOREHEADER, BITMAPINFOHEADER, BITMAPV4HEADER,
BITMAPV5HEADER.

Each line of output is prefixed with a number indicating the byte position in
the file. Some lines are purely informational, and don't correspond to any
actual data in the file, but they still begin with the current file position.

The output uses a mix of decimal, hexadecimal, and binary. Numbers that
represent color values or palette indices are in hexadecimal. "BITFIELD"
definition are in binary. Other numbers are generally in decimal, unless they
are prefixed with "0x" to indicate hexadecimal.


Row order
---------

Bmpinspect always calls the top row of the image "row 0". Most BMP images are
stored from bottom up, so the row number starts out high and finishes at 0.


Sample order
------------

BMP uses the order Blue-Green-Red for some things, and the more-usual Red-
Green-Blue for others. Also, it uses little-endian byte order, which means
that a number like 0x01020304 will appear in the file in the order 04 03 02
01, the reverse of the order when it is written out.

The palette always uses the order B-G-R (and there is usually an unused 4th
byte). When bmpinspect displays the palette, it reorders it to R-G-B.

24-bpp images always store their pixels in the order B-G-R. When bmpinspect
displays such a pixel, it uses the order R-G-B. For example "234567" means
R=0x23, G=0x45, B=0x67.

Items in R-G-B order include BITFIELDS definitions (the order in which the
definitions appear, that is), and Gamma and Endpoints fields.

The BITFIELDS definitions used by 16- and 32-bit images are 32-bit little-
endian integers. The samples can be in any order, but usually the highest-
order bits are used for red, and the lowest for blue. Because it's little-
endian, that means the blue bits appear first in the file.

The image data of a 16-bit image is interpreted as a sequence of 16-bit
little-endian integers, each representing one pixel, and interpreted according
to the BITFIELDS definition. If there is no BITFIELDS definition (i.e. the
"compression" field is BI_RGB), it means bit 15 is unused, bits 14-10 are Red,
9-5 are Green, and 4-0 are Blue.

The image data of a 32-bit image is interpreted as a sequence of 32-bit
little-endian integers, each representing one pixel, and interpreted according
to the BITFIELDS definition. If there is no BITFIELDS definition (i.e. the
"compression" is BI_RGB), it means bits 31-24 are unused, bits 23-16 are Red,
15-8 are Green, and 7-0 are Blue.


RLE compression
---------------

RLE-compressed rows are displayed as a sequence of items. An item can be:

 - An "uncompressed run", which starts with "u" and a decimal number of
pixels. The brackets contain the palette indices of those pixels.

 - A "compressed run", which starts with a decimal number of pixels. The
brackets contain one or two palette indices, which are to be repeated as many
times as necessary to assign a value to the given number of pixels. For
example, with a 4-bpp image, 5{67} would expand to 5 pixels, whose palette
indices are 0x6, 0x7, 0x6, 0x7, 0x6. With an 8-bpp image, 5{67} would instead
expand to palette indices 0x67, 0x67, 0x67, 0x67, 0x67.

- An EOL marker, marking the end of the current row.

- An EOBMP marker, marking the end of the image (and of the current row).

- (rare) A "DELTA" code, which changes the "current position" by an x- and
y-offset. RLE-compressed images are always stored from bottom-up, so a
nonzero y-offset *decreases* the row number assigned by bmpinspect.

Note that with 8-bpp images, palette indices are displayed separated with
spaces. With 4-bpp images, palette indices use only a single hex digit, so
there's no need to separate them with spaces.


Known limitations
-----------------

- bmpinspect doesn't know about embedded color profiles. (I've never seen a
BMP image with an embedded color profile, so it's hard to be sure what one
looks like.)

- bmpinspect doesn't inspect the contents of embedded JPEG or PNG images. Such
images are primarily for use with printers, and one would not expect to find
an actual BMP file with an embedded JPEG or PNG image.

- Does not OS/2v2-style BMPs (with a 64-byte header).

- Does not support Windows Mobile-style compression settings
"BI_ALPHABITFIELDS" and "BI_SRCPREROTATE".

