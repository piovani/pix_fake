package httpapi

import (
	"encoding/xml"
	"io"
	"net/http"
)

// Message versions of the catalog package spi.5.13.2. The split between messages
// accepted on input and sent on output follows the example in the Interfaces manual 1.12.
var (
	inCatalog = []string{
		"camt.060.spi.1.9",
		"pacs.002.spi.1.18",
		"pacs.004.spi.1.5",
		"pacs.008.spi.1.16",
		"pibr.001.spi.1.3",
		"trck.002.spi.1.1",
	}
	outCatalog = []string{
		"admi.002.spi.1.5",
		"camt.014.spi.1.6",
		"camt.052.spi.1.3",
		"camt.053.spi.1.4",
		"camt.054.spi.1.16",
		"pacs.002.spi.1.18",
		"pacs.004.spi.1.5",
		"pacs.008.spi.1.16",
		"pibr.002.spi.1.3",
		"reda.016.spi.1.5",
		"reda.017.spi.1.2",
	}
)

type catalogXML struct {
	XMLName xml.Name `xml:"Catalog"`
	Message []string `xml:"Message"`
}

// catalog is GET /api/v1/in/catalog and /api/v1/out/catalog.
func (s *Server) catalog(versions []string) handlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		out, err := xml.MarshalIndent(catalogXML{Message: versions}, "", "    ")
		if err != nil {
			return err
		}
		w.Header().Set("Content-Type", xmlType)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, xml.Header)
		_, _ = w.Write(out)
		return nil
	}
}
