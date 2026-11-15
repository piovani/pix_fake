package httpapi

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"

	"pixfake/mock/spi/internal/spi"
)

// send is POST /api/v1/in/{ispb}/msgs: one message, or up to 10 in multipart/mixed.
// The content is stored without validation, as the real API does at this point.
func (s *Server) send(w http.ResponseWriter, r *http.Request) error {
	ispb := r.PathValue("ispb")
	if err := s.checkISPB(ispb); err != nil {
		return err
	}
	if r.Header.Get("Content-Length") == "" && len(r.TransferEncoding) == 0 {
		return errLengthRequired
	}

	body, err := readBody(r)
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return errNoMessages
	}

	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return errMediaType
	}
	var bodies [][]byte
	switch mediaType {
	case "application/xml":
		if err := checkCharset(params["charset"]); err != nil {
			return err
		}
		bodies = [][]byte{body}
	case "multipart/mixed":
		bodies, err = splitMultipart(body, params["boundary"])
		if err != nil {
			return err
		}
	default:
		return errMediaType
	}

	ids, err := s.svc.Receive(r.Context(), ispb, bodies)
	if err != nil {
		return err
	}
	w.Header().Set("PI-ResourceId", strings.Join(ids, ","))
	w.WriteHeader(http.StatusCreated)
	return nil
}

// checkCharset: a missing charset is a media type error, a different one is a charset error.
func checkCharset(charset string) error {
	switch strings.ToLower(charset) {
	case "utf-8":
		return nil
	case "":
		return errMediaType
	default:
		return errCharset
	}
}

// readBody returns the request body, decompressed when Content-Encoding is gzip.
func readBody(r *http.Request) ([]byte, error) {
	var src io.Reader = r.Body
	switch strings.ToLower(r.Header.Get("Content-Encoding")) {
	case "", "identity":
	case "gzip":
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, errBadRequest
		}
		defer zr.Close()
		src = zr
	default:
		return nil, errMediaType
	}
	body, err := io.ReadAll(io.LimitReader(src, maxBodyBytes+1))
	if err != nil {
		return nil, errBadRequest
	}
	if len(body) > maxBodyBytes {
		return nil, errTooLarge
	}
	return body, nil
}

func splitMultipart(body []byte, boundary string) ([][]byte, error) {
	if boundary == "" {
		return nil, errBadRequest
	}
	mr := multipart.NewReader(bytes.NewReader(body), boundary)
	var parts [][]byte
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, errBadRequest
		}
		mediaType, params, err := mime.ParseMediaType(p.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/xml" {
			return nil, errMediaType
		}
		if err := checkCharset(params["charset"]); err != nil {
			return nil, err
		}
		b, err := io.ReadAll(p)
		if err != nil {
			return nil, errBadRequest
		}
		parts = append(parts, b)
		if len(parts) > spi.MaxMessages {
			return nil, errTooManyMsgs
		}
	}
	if len(parts) == 0 {
		return nil, errNoMessages
	}
	return parts, nil
}
