package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/k0kubun/pp"
)

// 7.5.4 Cross-Reference Table
type XrefEntry struct {
	ByteOffset int64
	Number     int64
	Generation int
	InUse      bool
}

// 7.5.5 Trailer
// trailer
// ...
// %%EOF
type Trailer struct {
	StartXref int64
	Size      int64
	Raw       []byte

	ra io.ReaderAt
}

func main() {
	pdff, err := os.Open(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	defer pdff.Close()

	fstat, err := pdff.Stat()
	if err != nil {
		log.Fatal(err)
	}

	tr, err := readTrailer(pdff, fstat.Size())
	if err != nil {
		log.Fatal(err)
	}

	switch os.Args[2] {
	case "show_trailer":
		pp.Println(tr)
		fmt.Println(string(tr.Raw))
		return
	case "show_xref_entry":
		entries, err := tr.ListXrefEntries()
		if err != nil {
			log.Fatal(err)
		}

		entryN, _ := strconv.Atoi(os.Args[3])
		if entryN == -1 {
			entryN = len(entries) - 1
		}

		entry, err := findXrefEntry(entries, int64(entryN), 0)
		if err != nil {
			log.Fatal(err)
		}

		b, err := readEntry(entry, pdff)
		if err != nil {
			log.Fatal(err)
		}

		fmt.Printf("%s", b)
	}
}

func readEntry(ent XrefEntry, ra io.ReaderAt) ([]byte, error) {
	ar := NewAtReader(ra, ent.ByteOffset)

	var b bytes.Buffer
	scanner := bufio.NewScanner(ar)
	scanner.Scan()
	l := scanner.Text()
	if !strings.HasSuffix(l, "obj") {
		return nil, errors.New("should start with a reference object")
	}

	b.WriteString(l + "\n")

	for scanner.Scan() {
		l := scanner.Text()
		if strings.EqualFold(l, "endobj") {
			b.WriteString(l + "\n")
			break
		}

		b.WriteString(l + "\n")
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("unable to read entry: %w", err)
	}

	return b.Bytes(), nil
}

func findXrefEntry(entries []XrefEntry, number int64, generation int) (XrefEntry, error) {
	for i := range entries {
		ent := entries[i]
		if ent.Number == number && ent.Generation == generation {
			return ent, nil
		}
	}

	return XrefEntry{}, errors.New("no entry found")
}

func readTrailer(ra io.ReaderAt, size int64) (Trailer, error) {
	buf := make([]byte, int(1024))
	tr := Trailer{
		ra: ra,
	}

	if _, err := ra.ReadAt(buf, size-int64(len(buf))); err != nil && err != io.EOF {
		return tr, err
	}

	if p := findTrailerInBlock(buf); p > 0 {
		buf = buf[p:]
	}
	tr.Raw = buf

	scanner := bufio.NewScanner(bytes.NewReader(buf))
	for scanner.Scan() {
		l := scanner.Text()
		if strings.HasPrefix(l, "startxref") {
			scanner.Scan()
			xref, err := strconv.ParseInt(scanner.Text(), 10, 64)
			if err != nil {
				return tr, fmt.Errorf("unable to parse startxref: %w", err)
			}
			tr.StartXref = xref
			continue
		}

		// FIXME
		const sizeEntry = "/Size"
		if n := strings.Index(l, sizeEntry+" "); n >= 0 {
			size, err := strconv.ParseInt(l[n+len(sizeEntry)+1:], 10, 64)
			if err != nil || size == 0 {
				return tr, fmt.Errorf("unable to parse /Size: %w", err)
			}
			tr.Size = size
		}
	}

	if err := scanner.Err(); err != nil {
		return tr, fmt.Errorf("unable to scan the trailer: %w", err)
	}

	return tr, nil
}

func (t Trailer) ListXrefEntries() ([]XrefEntry, error) {
	scanner := bufio.NewScanner(NewAtReader(t.ra, t.StartXref))

	scanner.Scan()
	if l := scanner.Text(); l != "xref" {
		return nil, fmt.Errorf("should be xref")
	}

	var entries []XrefEntry
	var offset, pos, count int
	var total int64
	var err error
	for scanner.Scan() {
		if t.Size == total {
			// read all entries in this xref table
			break
		}

		entry := strings.SplitN(scanner.Text(), " ", 3)

		if len(entry) == 2 {
			// this is a subsection
			offset, err = strconv.Atoi(entry[0])
			if err != nil {
				return nil, fmt.Errorf("unable to read xref subsection offset: %w", err)
			}
			count, err = strconv.Atoi(entry[1])
			if err != nil {
				return nil, fmt.Errorf("unable to read xref subsection count: %w", err)
			}

			// reset pos
			pos = 0
			continue
		}

		// skip since we have already read all entry in the subsection
		if pos == count {
			continue
		}

		// reading xref entry
		xrefEntry, err := readXrefEntry(entry)
		if err != nil {
			return nil, err
		}

		xrefEntry.Number = int64(offset + pos)
		entries = append(entries, xrefEntry)
		pos++
		total++
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("unable to scan the trailer: %w", err)
	}

	return entries, nil
}

func readXrefEntry(entry []string) (XrefEntry, error) {
	var xrefEntry XrefEntry

	offset, err := strconv.ParseInt(entry[0], 10, 64)
	if err != nil {
		return xrefEntry, fmt.Errorf("unable to read xref entry offset: %w", err)
	}
	xrefEntry.ByteOffset = offset

	generation, err := strconv.Atoi(entry[1])
	if err != nil {
		return xrefEntry, fmt.Errorf("unable to read xref entry generation: %w", err)
	}
	xrefEntry.Generation = generation

	if strings.HasPrefix(entry[2], "n") {
		xrefEntry.InUse = true
	}

	return xrefEntry, nil
}

func dumpAt(r io.ReaderAt, offset int64) {
	atReader := NewAtReader(r, offset)

	b, _ := io.ReadAll(atReader)
	fmt.Fprintf(os.Stderr, "%s", b)
}

func findTrailerInBlock(b []byte) int {
	return bytes.Index(b, []byte("trailer"))
}

type AtReader struct {
	ra     io.ReaderAt
	offset int64
}

func NewAtReader(ra io.ReaderAt, offset int64) *AtReader {
	return &AtReader{ra: ra, offset: offset}
}

func (ar *AtReader) Read(p []byte) (int, error) {
	n, err := ar.ra.ReadAt(p, ar.offset)
	if err != nil && err != io.EOF {
		return n, err
	}

	ar.offset += int64(n)
	return n, err
}
