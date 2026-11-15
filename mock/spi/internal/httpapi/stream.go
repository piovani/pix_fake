package httpapi

import (
	"compress/gzip"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"

	"pixfake/mock/spi/internal/spi"
)

// startStream is GET /api/v1/out/{ispb}/stream/start: it opens a stream and reads from it.
func (s *Server) startStream(w http.ResponseWriter, r *http.Request) error {
	ispb, multi, err := s.readParams(r)
	if err != nil {
		return err
	}
	token, err := s.svc.StartStream(r.Context(), ispb)
	if err != nil {
		return err
	}
	return s.pull(w, r, ispb, token, multi)
}

// stream is GET (read the next messages) and DELETE (close) on /api/v1/out/{ispb}/stream/{token}.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) error {
	ispb := r.PathValue("ispb")
	if !spi.ValidISPB(ispb) {
		return errInvalidISPB
	}
	token := r.PathValue("token")

	if r.Method == http.MethodDelete {
		if err := s.svc.CloseStream(r.Context(), ispb, token); err != nil {
			return streamError(err)
		}
		w.WriteHeader(http.StatusOK)
		return nil
	}
	_, multi, err := s.readParams(r)
	if err != nil {
		return err
	}
	return s.pull(w, r, ispb, token, multi)
}

// readParams validates the ISPB and the Accept header: multipart/mixed asks for several
// messages per response, application/xml (or none) for one.
func (s *Server) readParams(r *http.Request) (ispb string, multi bool, err error) {
	ispb = r.PathValue("ispb")
	if !spi.ValidISPB(ispb) {
		return "", false, errInvalidISPB
	}
	accept := r.Header.Get("Accept")
	if accept == "" {
		return ispb, false, nil
	}
	ok := false
	for _, v := range strings.Split(accept, ",") {
		mt, _, perr := mime.ParseMediaType(strings.TrimSpace(v))
		if perr != nil {
			continue
		}
		switch mt {
		case "multipart/mixed":
			return ispb, true, nil
		case "application/xml", "*/*":
			ok = true
		}
	}
	if !ok {
		return "", false, errNotAcceptable
	}
	return ispb, false, nil
}

func (s *Server) pull(w http.ResponseWriter, r *http.Request, ispb, token string, multi bool) error {
	limit := 1
	if multi {
		limit = spi.MaxMessages
	}
	msgs, next, err := s.svc.Pull(r.Context(), ispb, token, limit)
	if err != nil {
		return streamError(err)
	}
	w.Header().Set("PI-Pull-Next", BasePath+"/out/"+ispb+"/stream/"+next)
	if len(msgs) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return nil
	}

	out := io.Writer(w)
	if acceptsGzip(r) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		zw := gzip.NewWriter(w)
		defer zw.Close()
		out = zw
	}

	if !multi {
		w.Header().Set("PI-ResourceId", msgs[0].ResourceID)
		w.Header().Set("Content-Type", xmlType)
		w.WriteHeader(http.StatusOK)
		_, _ = out.Write(msgs[0].Body)
		return nil
	}

	mw := multipart.NewWriter(out)
	w.Header().Set("Content-Type", "multipart/mixed; boundary="+mw.Boundary())
	w.WriteHeader(http.StatusOK)
	for _, m := range msgs {
		part, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":  {xmlType},
			"PI-ResourceId": {m.ResourceID},
		})
		if err != nil {
			return nil // the response has already started
		}
		_, _ = part.Write(m.Body)
	}
	_ = mw.Close()
	return nil
}

func acceptsGzip(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept-Encoding")), "gzip")
}

func streamError(err error) error {
	if errors.Is(err, spi.ErrStreamGone) {
		return errGone
	}
	return err
}
