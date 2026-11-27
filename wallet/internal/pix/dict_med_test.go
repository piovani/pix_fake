package pix

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const reportXMLBody = `<InfractionReport><Id>r1</Id><TransactionId>E9900000220261127100000000000001</TransactionId>` +
	`<Reason>REFUND_REQUEST</Reason><SituationType>SCAM</SituationType><ReportDetails>golpe</ReportDetails><Status>OPEN</Status>` +
	`<ReporterParticipant>99000002</ReporterParticipant><CounterpartyParticipant>99000001</CounterpartyParticipant>` +
	`<InfractionAmount>150.00</InfractionAmount><LastModified>2026-11-27T10:00:00.000Z</LastModified></InfractionReport>`

const refundXMLBody = `<Refund><Id>f1</Id><TransactionId>E9900000220261127100000000000001</TransactionId><RefundReason>FRAUD</RefundReason>` +
	`<RefundAmount>20.50</RefundAmount><Status>OPEN</Status><RequestingParticipant>99000002</RequestingParticipant>` +
	`<ContestedParticipant>99000001</ContestedParticipant><InfractionReportId>r1</InfractionReportId>` +
	`<LastModified>2026-11-27T10:00:01.000Z</LastModified></Refund>`

// medServer answers each request with answer(r), after checking the headers every MED call carries.
func medServer(t *testing.T, answer func(r *http.Request, body string) (int, string)) *DictClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Participant-ISPB") != "99000001" || r.Header.Get("PI-RequestingParticipant") != "99000001" {
			t.Errorf("headers = %v", r.Header)
		}
		body, _ := io.ReadAll(r.Body)
		status, out := answer(r, string(body))
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, out)
	}))
	t.Cleanup(srv.Close)
	return NewDictClient(srv.URL+"/api/v2", "99000001")
}

func TestListInfractionReports(t *testing.T) {
	since := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	c := medServer(t, func(r *http.Request, _ string) (int, string) {
		q := r.URL.Query()
		if r.URL.Path != "/api/v2/infraction-reports" || q.Get("Participant") != "99000001" || q.Get("IsCounterparty") != "true" ||
			q.Get("ModifiedAfter") != "2026-11-27T09:00:00.000Z" || q.Get("Limit") != "200" || q.Get("IncludeDetails") != "true" {
			t.Errorf("request = %s", r.URL)
		}
		return 200, `<ListInfractionReportsResponse><HasMoreElements>true</HasMoreElements><InfractionReports>` + reportXMLBody + `</InfractionReports></ListInfractionReportsResponse>`
	})
	reports, more, err := c.ListInfractionReports(context.Background(), ListReportsParams{AsCounterparty: true, ModifiedAfter: since, Limit: 200})
	if err != nil || !more || len(reports) != 1 {
		t.Fatalf("list = %+v, %v, %v", reports, more, err)
	}
	r := reports[0]
	if r.ID != "r1" || r.AmountCents != 15000 || r.Reporter != "99000002" || r.Counterparty != "99000001" || r.Details != "golpe" ||
		!r.Modified.Equal(time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("report = %+v", r)
	}
}

func TestAcknowledgeAndCloseInfractionReport(t *testing.T) {
	c := medServer(t, func(r *http.Request, body string) (int, string) {
		switch r.URL.Path {
		case "/api/v2/infraction-reports/r1/acknowledge":
			if !strings.Contains(body, "<AcknowledgeInfractionReportRequest><InfractionReportId>r1</InfractionReportId><Participant>99000001</Participant>") {
				t.Errorf("acknowledge body = %s", body)
			}
		case "/api/v2/infraction-reports/r1/close":
			if !strings.Contains(body, "<FraudType>MULE_ACCOUNT</FraudType><AnalysisResult>AGREED</AnalysisResult><AnalysisDetails>ok</AnalysisDetails>") {
				t.Errorf("close body = %s", body)
			}
		default:
			t.Errorf("path = %s", r.URL.Path)
		}
		return 200, `<Response>` + reportXMLBody + `</Response>`
	})
	if r, err := c.AcknowledgeInfractionReport(context.Background(), "r1"); err != nil || r.ID != "r1" {
		t.Fatalf("acknowledge = %+v, %v", r, err)
	}
	if r, err := c.CloseInfractionReport(context.Background(), "r1", "AGREED", "MULE_ACCOUNT", "ok"); err != nil || r.ID != "r1" {
		t.Fatalf("close = %+v, %v", r, err)
	}
}

func TestRefunds(t *testing.T) {
	c := medServer(t, func(r *http.Request, body string) (int, string) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/refunds":
			q := r.URL.Query()
			if q.Get("ParticipantRole") != "CONTESTED" || q.Get("Status") != "OPEN" || q.Get("Cursor") != "c1" {
				t.Errorf("list = %s", r.URL)
			}
			return 200, `<ListRefundsResponse><HasMoreElements>true</HasMoreElements><NextCursor>c2</NextCursor><Refunds>` + refundXMLBody + `</Refunds></ListRefundsResponse>`
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/refunds/f1":
			return 200, `<GetRefundResponse>` + refundXMLBody + `</GetRefundResponse>`
		case r.URL.Path == "/api/v2/refunds/f1/close":
			if !strings.Contains(body, "<RefundAnalysisResult>TOTALLY_ACCEPTED</RefundAnalysisResult><RefundTransactionId>D99000001202611271000abcdefghijk</RefundTransactionId>") {
				t.Errorf("close body = %s", body)
			}
			return 200, `<CloseRefundResponse>` + refundXMLBody + `</CloseRefundResponse>`
		}
		t.Errorf("request = %s %s", r.Method, r.URL)
		return 404, ""
	})
	ctx := context.Background()
	page, err := c.ListRefunds(ctx, ListRefundsParams{AsContested: true, Statuses: []string{"OPEN"}, Cursor: "c1", ModifiedAfter: time.Now(), Limit: 10})
	if err != nil || !page.HasMore || page.NextCursor != "c2" || len(page.Refunds) != 1 {
		t.Fatalf("page = %+v, %v", page, err)
	}
	if f := page.Refunds[0]; f.ID != "f1" || f.AmountCents != 2050 || f.Contested != "99000001" || f.ReportID != "r1" || f.Reason != "FRAUD" {
		t.Fatalf("refund = %+v", f)
	}
	if f, err := c.GetRefund(ctx, "f1"); err != nil || f.Status != "OPEN" {
		t.Fatalf("get = %+v, %v", f, err)
	}
	if _, err := c.CloseRefund(ctx, CloseRefundParams{ID: "f1", Result: "TOTALLY_ACCEPTED", RefundTransactionID: "D99000001202611271000abcdefghijk"}); err != nil {
		t.Fatal(err)
	}
}

func TestMEDErrors(t *testing.T) {
	c := medServer(t, func(r *http.Request, _ string) (int, string) {
		if strings.HasSuffix(r.URL.Path, "/f2") {
			return 200, `<GetRefundResponse><Refund><Id>f2</Id><RefundAmount>abc</RefundAmount></Refund></GetRefundResponse>`
		}
		if strings.HasSuffix(r.URL.Path, "/f3") {
			return 200, `<GetRefundResponse><Refund><Id>f3</Id><LastModified>yesterday</LastModified></Refund></GetRefundResponse>`
		}
		return 403, `<problem><type>https://dict.pi.rsfn.net.br/api/v2/error/Forbidden</type><title>Forbidden</title></problem>`
	})
	ctx := context.Background()
	if _, _, err := c.ListInfractionReports(ctx, ListReportsParams{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("list: %v", err)
	}
	if _, err := c.ListRefunds(ctx, ListRefundsParams{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("list refunds: %v", err)
	}
	if _, err := c.GetRefund(ctx, "f2"); err == nil {
		t.Fatal("a bad amount was accepted")
	}
	if _, err := c.GetRefund(ctx, "f3"); err == nil {
		t.Fatal("a bad time was accepted")
	}
}

func TestCreateAndCancelInfractionReportAndCreateRefund(t *testing.T) {
	c := medServer(t, func(r *http.Request, body string) (int, string) {
		switch r.URL.Path {
		case "/api/v2/infraction-reports":
			if !strings.Contains(body, "<CreateInfractionReportRequest><Participant>99000001</Participant><InfractionReport><TransactionId>E1</TransactionId>"+
				"<Reason>REFUND_REQUEST</Reason><SituationType>SCAM</SituationType><ReportDetails>golpe</ReportDetails>") {
				t.Errorf("create body = %s", body)
			}
			return 201, `<CreateInfractionReportResponse>` + reportXMLBody + `</CreateInfractionReportResponse>`
		case "/api/v2/infraction-reports/r1/cancel":
			if !strings.Contains(body, "<CancelInfractionReportRequest><InfractionReportId>r1</InfractionReportId>") {
				t.Errorf("cancel body = %s", body)
			}
			return 200, `<CancelInfractionReportResponse>` + reportXMLBody + `</CancelInfractionReportResponse>`
		case "/api/v2/refunds":
			if !strings.Contains(body, "<TransactionId>E1</TransactionId><RefundReason>FRAUD</RefundReason><RefundAmount>45.00</RefundAmount>") {
				t.Errorf("refund body = %s", body)
			}
			return 201, `<CreateRefundResponse>` + refundXMLBody + `</CreateRefundResponse>`
		}
		t.Errorf("path = %s", r.URL.Path)
		return 404, ""
	})
	ctx := context.Background()
	if r, err := c.CreateInfractionReport(ctx, NewReport{TransactionID: "E1", SituationType: "SCAM", Details: "golpe"}); err != nil || r.ID != "r1" {
		t.Fatalf("create = %+v, %v", r, err)
	}
	if r, err := c.CancelInfractionReport(ctx, "r1"); err != nil || r.ID != "r1" {
		t.Fatalf("cancel = %+v, %v", r, err)
	}
	if f, err := c.CreateRefund(ctx, "E1", "FRAUD", 4500, ""); err != nil || f.ID != "f1" {
		t.Fatalf("refund = %+v, %v", f, err)
	}
}

func TestListMyReportsAndClosedRefund(t *testing.T) {
	c := medServer(t, func(r *http.Request, _ string) (int, string) {
		if r.URL.Path == "/api/v2/infraction-reports" {
			if q := r.URL.Query(); q.Get("IsReporter") != "true" || q.Get("Status") != "OPEN,ACKNOWLEDGED" || q.Has("IsCounterparty") {
				t.Errorf("list = %s", r.URL)
			}
			return 200, `<ListInfractionReportsResponse><HasMoreElements>false</HasMoreElements></ListInfractionReportsResponse>`
		}
		return 200, `<GetRefundResponse><Refund><Id>f1</Id><RefundAmount>1.00</RefundAmount><Status>CLOSED</Status>` +
			`<AnalysisResult>REJECTED</AnalysisResult><RefundRejectionReason>NO_BALANCE</RefundRejectionReason></Refund></GetRefundResponse>`
	})
	if _, _, err := c.ListInfractionReports(context.Background(), ListReportsParams{AsReporter: true, Statuses: []string{"OPEN", "ACKNOWLEDGED"}}); err != nil {
		t.Fatal(err)
	}
	f, err := c.GetRefund(context.Background(), "f1")
	if err != nil || f.AnalysisResult != "REJECTED" || f.RejectionReason != "NO_BALANCE" {
		t.Fatalf("refund = %+v, %v", f, err)
	}
}
