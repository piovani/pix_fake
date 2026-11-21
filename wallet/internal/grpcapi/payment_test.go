package grpcapi

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	walletv1 "pixfake/wallet/gen/wallet/v1"
	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/payment"
	"pixfake/wallet/internal/pix"
)

type fakePayments struct {
	resolved   [2]string
	paid       payment.PayInput
	listed     payment.ListInput
	resolution payment.Resolution
	payment    payment.Payment
	page       payment.Page
	err        error
}

func (f *fakePayments) ResolveKey(_ context.Context, accountID, key string) (payment.Resolution, error) {
	f.resolved = [2]string{accountID, key}
	return f.resolution, f.err
}

func (f *fakePayments) Pay(_ context.Context, in payment.PayInput) (payment.Payment, error) {
	f.paid = in
	return f.payment, f.err
}

func (f *fakePayments) Get(context.Context, string) (payment.Payment, error) { return f.payment, f.err }

func (f *fakePayments) List(_ context.Context, in payment.ListInput) (payment.Page, error) {
	f.listed = in
	return f.page, f.err
}

func ctx5(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return c
}

var testPayee = payment.Payee{
	Name: "Carla Dias", TaxID: "39053344705", Participant: "99000002", Branch: "0001", AccountNumber: "00456789",
	AccountType: pix.AccountChecking, Key: "carla@example.com",
}

func TestResolveKeyAndCreatePayment(t *testing.T) {
	created := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	f := &fakePayments{
		resolution: payment.Resolution{ID: "res-1", Payee: testPayee},
		payment: payment.Payment{
			ID: "pay-1", AccountID: "acc", Direction: payment.Outbound, Type: payment.TypePix, Status: payment.Pending,
			AmountCents: 2590, EndToEndID: "E99000001202611211200abcdefghijk", Counterparty: testPayee, Message: "lunch", CreatedAt: created,
		},
	}
	client := walletv1.NewPaymentServiceClient(dialAll(t, fakeLedger{}, nil, f))

	r, err := client.ResolveKey(ctx5(t), &walletv1.ResolveKeyRequest{AccountId: "acc", Key: "carla@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if f.resolved != [2]string{"acc", "carla@example.com"} || r.GetResolutionId() != "res-1" || r.GetBookTransfer() {
		t.Fatalf("resolved %v: %v", f.resolved, r)
	}
	p := r.GetPayee()
	if p.GetName() != "Carla Dias" || p.GetTaxId() != "39053344705" || p.GetParticipant() != "99000002" || p.GetAccountType() != "CACC" || p.GetKey() != "carla@example.com" {
		t.Errorf("payee = %v", p)
	}

	res, err := client.CreatePayment(ctx5(t), &walletv1.CreatePaymentRequest{ResolutionId: "res-1", AmountCents: 2590, Message: "lunch"})
	if err != nil {
		t.Fatal(err)
	}
	if f.paid != (payment.PayInput{ResolutionID: "res-1", AmountCents: 2590, Message: "lunch"}) {
		t.Fatalf("paid %+v", f.paid)
	}
	got := res.GetPayment()
	if got.GetId() != "pay-1" || got.GetDirection() != walletv1.PaymentDirection_PAYMENT_DIRECTION_OUTBOUND ||
		got.GetType() != walletv1.PaymentType_PAYMENT_TYPE_PIX || got.GetStatus() != walletv1.PaymentStatus_PAYMENT_STATUS_PENDING ||
		got.GetAmountCents() != 2590 || got.GetEndToEndId() != "E99000001202611211200abcdefghijk" || got.GetMessage() != "lunch" ||
		got.GetCounterparty().GetName() != "Carla Dias" || !got.GetCreatedAt().AsTime().Equal(created) || got.GetFinishedAt() != nil {
		t.Errorf("payment = %v", got)
	}
}

func TestGetPaymentShowsARejection(t *testing.T) {
	finished := time.Date(2026, 11, 21, 12, 0, 5, 0, time.UTC)
	f := &fakePayments{payment: payment.Payment{
		ID: "pay-1", Status: payment.Rejected, RejectCode: "AC07", RejectReason: "The receiving account is closed.", FinishedAt: finished,
	}}
	res, err := walletv1.NewPaymentServiceClient(dialAll(t, fakeLedger{}, nil, f)).GetPayment(ctx5(t), &walletv1.GetPaymentRequest{PaymentId: "pay-1"})
	if err != nil {
		t.Fatal(err)
	}
	p := res.GetPayment()
	if p.GetStatus() != walletv1.PaymentStatus_PAYMENT_STATUS_REJECTED || p.GetRejectCode() != "AC07" || p.GetRejectReason() == "" || !p.GetFinishedAt().AsTime().Equal(finished) {
		t.Errorf("payment = %v", p)
	}
}

func TestListPayments(t *testing.T) {
	f := &fakePayments{page: payment.Page{
		Payments:  []payment.Payment{{ID: "a", Status: payment.Settled}, {ID: "b", Status: payment.Pending}},
		NextToken: "next",
	}}
	client := walletv1.NewPaymentServiceClient(dialAll(t, fakeLedger{}, nil, f))

	res, err := client.ListPayments(ctx5(t), &walletv1.ListPaymentsRequest{
		AccountId: "acc", PageSize: 2, PageToken: "tok",
		Statuses: []walletv1.PaymentStatus{walletv1.PaymentStatus_PAYMENT_STATUS_PENDING, walletv1.PaymentStatus_PAYMENT_STATUS_UNSPECIFIED, walletv1.PaymentStatus_PAYMENT_STATUS_REJECTED},
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.listed.AccountID != "acc" || f.listed.PageSize != 2 || f.listed.PageToken != "tok" ||
		len(f.listed.Statuses) != 2 || f.listed.Statuses[0] != payment.Pending || f.listed.Statuses[1] != payment.Rejected {
		t.Errorf("listed %+v", f.listed)
	}
	if len(res.GetPayments()) != 2 || res.GetPayments()[0].GetId() != "a" || res.GetNextPageToken() != "next" ||
		res.GetPayments()[0].GetStatus() != walletv1.PaymentStatus_PAYMENT_STATUS_SETTLED {
		t.Errorf("response = %v", res)
	}
}

func TestPaymentErrors(t *testing.T) {
	tests := []struct {
		err  error
		want codes.Code
	}{
		{payment.ErrInvalid, codes.InvalidArgument},
		{ledger.ErrAccountNotFound, codes.NotFound},
		{payment.ErrKeyNotFound, codes.NotFound},
		{payment.ErrResolutionNotFound, codes.NotFound},
		{payment.ErrPaymentNotFound, codes.NotFound},
		{payment.ErrResolutionConflict, codes.AlreadyExists},
		{ledger.ErrInsufficientFunds, codes.FailedPrecondition},
		{payment.ErrBookTransferUnsupported, codes.Unimplemented},
		{&pix.APIError{Status: 429}, codes.ResourceExhausted},
		{&pix.APIError{Status: 503}, codes.Unavailable},
		{errors.New("connection refused to 10.0.0.5"), codes.Internal},
	}
	for _, tt := range tests {
		client := walletv1.NewPaymentServiceClient(dialAll(t, fakeLedger{}, nil, &fakePayments{err: tt.err}))
		_, e1 := client.ResolveKey(ctx5(t), &walletv1.ResolveKeyRequest{})
		_, e2 := client.CreatePayment(ctx5(t), &walletv1.CreatePaymentRequest{})
		_, e3 := client.GetPayment(ctx5(t), &walletv1.GetPaymentRequest{})
		_, e4 := client.ListPayments(ctx5(t), &walletv1.ListPaymentsRequest{})
		for _, err := range []error{e1, e2, e3, e4} {
			if status.Code(err) != tt.want || strings.Contains(err.Error(), "10.0.0.5") {
				t.Errorf("%v: want %v, got %v", tt.err, tt.want, err)
			}
		}
	}
}

func TestPaymentServiceIsOptionalAndReturnIsNotBuiltYet(t *testing.T) {
	if _, err := walletv1.NewPaymentServiceClient(dial(t, fakeLedger{})).GetPayment(ctx5(t), &walletv1.GetPaymentRequest{}); status.Code(err) != codes.Unimplemented {
		t.Errorf("without the module: %v", err)
	}
	client := walletv1.NewPaymentServiceClient(dialAll(t, fakeLedger{}, nil, &fakePayments{}))
	if _, err := client.ReturnPayment(ctx5(t), &walletv1.ReturnPaymentRequest{}); status.Code(err) != codes.Unimplemented {
		t.Errorf("ReturnPayment: %v", err)
	}
}
