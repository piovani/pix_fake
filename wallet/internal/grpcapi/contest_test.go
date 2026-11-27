package grpcapi

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	walletv1 "pixfake/wallet/gen/wallet/v1"
	"pixfake/wallet/internal/med"
	"pixfake/wallet/internal/payment"
	"pixfake/wallet/internal/pix"
)

type fakeContests struct {
	contest med.Contest
	err     error
	in      med.ContestInput
}

func (f *fakeContests) Contest(_ context.Context, in med.ContestInput) (med.Contest, error) {
	f.in = in
	return f.contest, f.err
}
func (f *fakeContests) GetContest(context.Context, string) (med.Contest, error) {
	return f.contest, f.err
}
func (f *fakeContests) Contests(context.Context, string) ([]med.Contest, error) {
	return []med.Contest{f.contest}, f.err
}
func (f *fakeContests) CancelContest(context.Context, string) (med.Contest, error) {
	return f.contest, f.err
}

func dialContests(t *testing.T, c Contests) walletv1.ContestServiceClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := New(fakeLedger{}, nil, nil)
	RegisterContests(srv, c)
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return walletv1.NewContestServiceClient(conn)
}

func TestContestPayment(t *testing.T) {
	at := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	f := &fakeContests{contest: med.Contest{
		ID: "c1", AccountID: "a1", PaymentID: "p1", Situation: "ACCOUNT_TAKEOVER", Details: "d", Status: med.ContestAgreed,
		AmountCents: 500, RefundedCents: 0, CreatedAt: at, UpdatedAt: at,
	}}
	client := dialContests(t, f)
	res, err := client.ContestPayment(ctx5(t), &walletv1.ContestPaymentRequest{
		PaymentId: "p1", Situation: walletv1.ContestSituation_CONTEST_SITUATION_ACCOUNT_TAKEOVER, Details: "d", RequestId: "r1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.in != (med.ContestInput{PaymentID: "p1", Situation: "ACCOUNT_TAKEOVER", Details: "d", RequestID: "r1"}) {
		t.Fatalf("input = %+v", f.in)
	}
	c := res.Contest
	if c.Id != "c1" || c.Situation != walletv1.ContestSituation_CONTEST_SITUATION_ACCOUNT_TAKEOVER ||
		c.Status != walletv1.ContestStatus_CONTEST_STATUS_REFUND_REQUESTED || c.AmountCents != 500 || !c.CreatedAt.AsTime().Equal(at) {
		t.Fatalf("contest = %v", c)
	}
	if _, err := client.ContestPayment(ctx5(t), &walletv1.ContestPaymentRequest{}); err != nil || f.in.Situation != "" {
		t.Fatalf("no situation: %v, %+v", err, f.in)
	}

	f.contest.Status, f.contest.RefundedCents, f.contest.ReturnID = med.ContestRefunded, 500, "D1"
	got, err := client.GetContest(ctx5(t), &walletv1.GetContestRequest{ContestId: "c1"})
	if err != nil || got.Contest.Status != walletv1.ContestStatus_CONTEST_STATUS_REFUNDED || got.Contest.ReturnEndToEndId != "D1" {
		t.Fatalf("get = %v, %v", got, err)
	}
	list, err := client.ListContests(ctx5(t), &walletv1.ListContestsRequest{AccountId: "a1"})
	if err != nil || len(list.Contests) != 1 {
		t.Fatalf("list = %v, %v", list, err)
	}
	f.contest.Status = med.ContestCancelled
	if res, err := client.CancelContest(ctx5(t), &walletv1.CancelContestRequest{ContestId: "c1"}); err != nil ||
		res.Contest.Status != walletv1.ContestStatus_CONTEST_STATUS_CANCELLED {
		t.Fatalf("cancel = %v, %v", res, err)
	}
}

func TestContestErrors(t *testing.T) {
	tests := []struct {
		err  error
		want codes.Code
	}{
		{med.ErrInvalid, codes.InvalidArgument},
		{payment.ErrPaymentNotFound, codes.NotFound},
		{med.ErrContestNotFound, codes.NotFound},
		{med.ErrNotContestable, codes.FailedPrecondition},
		{med.ErrAlreadyContested, codes.FailedPrecondition},
		{med.ErrNotCancellable, codes.FailedPrecondition},
		{med.ErrRequestConflict, codes.AlreadyExists},
		{pix.ErrRateLimited, codes.ResourceExhausted},
		{pix.ErrUnavailable, codes.Unavailable},
		{errors.New("boom"), codes.Internal},
	}
	for _, tt := range tests {
		f := &fakeContests{err: tt.err}
		client := dialContests(t, f)
		_, err := client.ContestPayment(ctx5(t), &walletv1.ContestPaymentRequest{})
		if status.Code(err) != tt.want {
			t.Errorf("%v: got %v, want %v", tt.err, err, tt.want)
		}
		for _, call := range []func() error{
			func() error { _, err := client.GetContest(ctx5(t), &walletv1.GetContestRequest{}); return err },
			func() error { _, err := client.ListContests(ctx5(t), &walletv1.ListContestsRequest{}); return err },
			func() error { _, err := client.CancelContest(ctx5(t), &walletv1.CancelContestRequest{}); return err },
		} {
			if status.Code(call()) != tt.want {
				t.Errorf("%v: want %v", tt.err, tt.want)
			}
		}
	}
}
