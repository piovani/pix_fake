package grpcapi

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	walletv1 "pixfake/wallet/gen/wallet/v1"
	"pixfake/wallet/internal/med"
	"pixfake/wallet/internal/payment"
	"pixfake/wallet/internal/pix"
)

// Contests is what the API needs from the med module.
type Contests interface {
	Contest(ctx context.Context, in med.ContestInput) (med.Contest, error)
	GetContest(ctx context.Context, id string) (med.Contest, error)
	Contests(ctx context.Context, accountID string) ([]med.Contest, error)
	CancelContest(ctx context.Context, id string) (med.Contest, error)
}

// RegisterContests serves wallet.v1.ContestService on s.
func RegisterContests(s *grpc.Server, c Contests) {
	walletv1.RegisterContestServiceServer(s, &contestService{contests: c})
}

type contestService struct {
	walletv1.UnimplementedContestServiceServer
	contests Contests
}

func (s *contestService) ContestPayment(ctx context.Context, req *walletv1.ContestPaymentRequest) (*walletv1.ContestPaymentResponse, error) {
	situation := ""
	if req.GetSituation() != walletv1.ContestSituation_CONTEST_SITUATION_UNSPECIFIED {
		situation = strings.TrimPrefix(req.GetSituation().String(), "CONTEST_SITUATION_")
	}
	c, err := s.contests.Contest(ctx, med.ContestInput{
		PaymentID: req.GetPaymentId(), Situation: situation, Details: req.GetDetails(), RequestID: req.GetRequestId(),
	})
	if err != nil {
		return nil, contestError("contest payment", err)
	}
	return &walletv1.ContestPaymentResponse{Contest: toProtoContest(c)}, nil
}

func (s *contestService) GetContest(ctx context.Context, req *walletv1.GetContestRequest) (*walletv1.GetContestResponse, error) {
	c, err := s.contests.GetContest(ctx, req.GetContestId())
	if err != nil {
		return nil, contestError("get contest", err)
	}
	return &walletv1.GetContestResponse{Contest: toProtoContest(c)}, nil
}

func (s *contestService) ListContests(ctx context.Context, req *walletv1.ListContestsRequest) (*walletv1.ListContestsResponse, error) {
	list, err := s.contests.Contests(ctx, req.GetAccountId())
	if err != nil {
		return nil, contestError("list contests", err)
	}
	res := &walletv1.ListContestsResponse{}
	for _, c := range list {
		res.Contests = append(res.Contests, toProtoContest(c))
	}
	return res, nil
}

func (s *contestService) CancelContest(ctx context.Context, req *walletv1.CancelContestRequest) (*walletv1.CancelContestResponse, error) {
	c, err := s.contests.CancelContest(ctx, req.GetContestId())
	if err != nil {
		return nil, contestError("cancel contest", err)
	}
	return &walletv1.CancelContestResponse{Contest: toProtoContest(c)}, nil
}

func contestError(op string, err error) error {
	switch {
	case errors.Is(err, med.ErrInvalid):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, payment.ErrPaymentNotFound):
		return status.Error(codes.NotFound, "payment not found")
	case errors.Is(err, med.ErrContestNotFound):
		return status.Error(codes.NotFound, "contest not found")
	case errors.Is(err, med.ErrNotContestable), errors.Is(err, med.ErrAlreadyContested), errors.Is(err, med.ErrNotCancellable):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, med.ErrRequestConflict):
		return status.Error(codes.AlreadyExists, "request_id reused with other data")
	case errors.Is(err, pix.ErrRateLimited):
		return status.Error(codes.ResourceExhausted, "dict rate limit, try again later")
	case errors.Is(err, pix.ErrUnavailable):
		return status.Error(codes.Unavailable, "dict unavailable, try again later")
	}
	slog.Error(op, "err", err)
	return status.Error(codes.Internal, "internal error")
}

var contestStatuses = map[string]walletv1.ContestStatus{
	med.ContestOpening:         walletv1.ContestStatus_CONTEST_STATUS_OPENING,
	med.ContestReported:        walletv1.ContestStatus_CONTEST_STATUS_REPORTED,
	med.ContestAgreed:          walletv1.ContestStatus_CONTEST_STATUS_REFUND_REQUESTED, // the refund is on its way
	med.ContestRefundRequested: walletv1.ContestStatus_CONTEST_STATUS_REFUND_REQUESTED,
	med.ContestRefunded:        walletv1.ContestStatus_CONTEST_STATUS_REFUNDED,
	med.ContestRejected:        walletv1.ContestStatus_CONTEST_STATUS_REJECTED,
	med.ContestCancelled:       walletv1.ContestStatus_CONTEST_STATUS_CANCELLED,
}

func toProtoContest(c med.Contest) *walletv1.Contest {
	return &walletv1.Contest{
		Id: c.ID, AccountId: c.AccountID, PaymentId: c.PaymentID,
		Situation: walletv1.ContestSituation(walletv1.ContestSituation_value["CONTEST_SITUATION_"+c.Situation]),
		Details:   c.Details, Status: contestStatuses[c.Status], AmountCents: c.AmountCents, RefundedCents: c.RefundedCents,
		ReturnEndToEndId: c.ReturnID, Rejection: c.Rejection,
		CreatedAt: timestamppb.New(c.CreatedAt), UpdatedAt: timestamppb.New(c.UpdatedAt),
	}
}
