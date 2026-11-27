package netex

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"golang.org/x/net/html/charset"
)

// Schedule holds the stop catalogue and public transport timetable parts of a
// NeTEx publication. Documents in one archive are indexed together because
// references commonly cross XML files.
type Schedule struct {
	stopData
	scheduleData
}

func (s *Schedule) reset() {
	s.stopData = stopData{}
	s.scheduleData = scheduleData{}
}

// ParseFile indexes a single NeTEx XML document.
func (s *Schedule) ParseFile(reader io.Reader) error {
	s.reset()
	recognized, err := s.parseXML(reader, "document")
	if err != nil {
		return err
	}
	if !recognized {
		return fmt.Errorf("parse NeTEx XML %q: document contains no recognized NeTEx data", "document")
	}
	return nil
}

// ParseArchive indexes XML files in a ZIP archive. Directories and ancillary
// files are ignored; every XML document is parsed and must be recognizable.
func (s *Schedule) ParseArchive(reader io.ReaderAt, size int64) error {
	s.reset()
	archive, err := zip.NewReader(reader, size)
	if err != nil {
		return fmt.Errorf("open NeTEx archive: %w", err)
	}
	xmlFiles := 0
	recognizedDocuments := 0
	for _, file := range archive.File {
		if file.FileInfo().IsDir() || !strings.EqualFold(filepath.Ext(file.Name), ".xml") {
			continue
		}
		xmlFiles++
		stream, err := file.Open()
		if err != nil {
			return fmt.Errorf("open NeTEx archive entry %q: %w", file.Name, err)
		}
		recognized, parseErr := s.parseXML(stream, file.Name)
		closeErr := stream.Close()
		if parseErr != nil {
			return parseErr
		}
		if closeErr != nil {
			return fmt.Errorf("close NeTEx archive entry %q: %w", file.Name, closeErr)
		}
		if recognized {
			recognizedDocuments++
		}
	}
	if xmlFiles == 0 {
		return fmt.Errorf("NeTEx archive contains no XML documents")
	}
	if recognizedDocuments == 0 {
		return fmt.Errorf("NeTEx archive contains no recognized NeTEx data")
	}
	return nil
}

func (s *Schedule) parseXML(reader io.Reader, label string) (bool, error) {
	decoder := xml.NewDecoder(reader)
	decoder.CharsetReader = charset.NewReaderLabel
	recognized := false
	rootSeen := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return false, fmt.Errorf("parse NeTEx XML %q: %w", label, err)
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		if !rootSeen {
			rootSeen = true
		}
		consumed, err := s.stopData.decodeStopElement(decoder, start)
		if err != nil {
			return false, fmt.Errorf("parse NeTEx stops in %q: %w", label, err)
		}
		if consumed {
			recognized = true
			continue
		}
		consumed, err = s.scheduleData.decodeScheduleElement(decoder, start)
		if err != nil {
			return false, fmt.Errorf("parse NeTEx schedule in %q: %w", label, err)
		}
		if consumed {
			recognized = true
		}
	}
	if !rootSeen {
		return false, fmt.Errorf("parse NeTEx XML %q: document is empty", label)
	}
	return recognized, nil
}
