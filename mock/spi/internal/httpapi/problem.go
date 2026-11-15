package httpapi

import (
	"encoding/xml"
	"net/http"
)

const problemTypeBase = "https://icom.pi.rsfn.net.br/api/v1/error/"

// apiError is an error the API reports as application/problem+xml (RFC 7807).
type apiError struct {
	status int
	slug   string // last part of the problem type URL
	title  string
}

func (e *apiError) Error() string { return e.slug + ": " + e.title }

var (
	errBadRequest     = &apiError{http.StatusBadRequest, "bad-request", "Requisição inválida."}
	errInvalidISPB    = &apiError{http.StatusBadRequest, "ispb", "ISPB inválido."}
	errCharset        = &apiError{http.StatusBadRequest, "charset", "Charset XML inválido."}
	errNoMessages     = &apiError{http.StatusBadRequest, "no-messages", "Nenhuma mensagem enviada."}
	errTooManyMsgs    = &apiError{http.StatusBadRequest, "too-many-messages", "Número máximo de mensagens por requisição excedido."}
	errForbidden      = &apiError{http.StatusForbidden, "forbidden", "Acesso não permitido."}
	errNotFound       = &apiError{http.StatusNotFound, "not-found", "Recurso não encontrado."}
	errMethod         = &apiError{http.StatusMethodNotAllowed, "method-not-allowed", "Método não permitido."}
	errNotAcceptable  = &apiError{http.StatusNotAcceptable, "not-acceptable", "Tipo de conteúdo solicitado não é aceito."}
	errGone           = &apiError{http.StatusGone, "gone", "Recurso removido."}
	errLengthRequired = &apiError{http.StatusLengthRequired, "length-required", "Content-Length ou Transfer-Encoding é obrigatório."}
	errTooLarge       = &apiError{http.StatusRequestEntityTooLarge, "payload-too-large", "Conteúdo da requisição muito extenso."}
	errMediaType      = &apiError{http.StatusUnsupportedMediaType, "media-type", "Content-Type ou Content-Encoding inválido."}
	errUnavailable    = &apiError{http.StatusServiceUnavailable, "unavailable", "Serviço indisponível."}
)

// problemXML is the RFC 7807 body as shown in the Interfaces manual: only type and title.
type problemXML struct {
	XMLName xml.Name `xml:"urn:ietf:rfc:7807 problem"`
	Type    string   `xml:"type"`
	Title   string   `xml:"title"`
}

func newProblem(e *apiError) problemXML {
	return problemXML{Type: problemTypeBase + e.slug, Title: e.title}
}
