package grpcapi

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	walletv1 "pixfake/wallet/gen/wallet/v1"
	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/payment"
	"pixfake/wallet/internal/pix"
)

// Payments is what the API needs from the payment module.
type Payments interface {
	ResolveKey(ctx context.Context, accountID, key string) (payment.Resolution, error)
	Pay(ctx context.Context, in payment.PayInput) (payment.Payment, error)
	Get(ctx context.Context, id string) (payment.Payment, error)
	List(ctx context.Context, in payment.ListInput) (payment.Page, error)
	Return(ctx context.Context, in payment.ReturnInput) (payment.Payment, error)
	Statement(ctx context.Context, in payment.StatementInput) (payment.Statement, error)
}

// paymentService answers wallet.v1.PaymentService.
type paymentService struct {
	walletv1.UnimplementedPaymentServiceServer
	payments Payments
}

func (s *paymentService) ResolveKey(ctx context.Context, req *walletv1.ResolveKeyRequest) (*walletv1.ResolveKeyResponse, error) {
	r, err := s.payments.ResolveKey(ctx, req.GetAccountId(), req.GetKey())
	if err != nil {
		return nil, paymentError("resolve key", err)
	}
	return &walletv1.ResolveKeyResponse{ResolutionId: r.ID, Payee: toProtoPayee(r.Payee), BookTransfer: r.BookTransfer}, nil
}

func (s *paymentService) CreatePayment(ctx context.Context, req *walletv1.CreatePaymentRequest) (*walletv1.CreatePaymentResponse, error) {
	p, err := s.payments.Pay(ctx, payment.PayInput{
		ResolutionID: req.GetResolutionId(), AmountCents: req.GetAmountCents(), Message: req.GetMessage(),
	})
	if err != nil {
		return nil, paymentError("create payment", err)
	}
	return &walletv1.CreatePaymentResponse{Payment: toProtoPayment(p)}, nil
}

func (s *paymentService) GetPayment(ctx context.Context, req *walletv1.GetPaymentRequest) (*walletv1.GetPaymentResponse, error) {
	p, err := s.payments.Get(ctx, req.GetPaymentId())
	if err != nil {
		return nil, paymentError("get payment", err)
	}
	return &walletv1.GetPaymentResponse{Payment: toProtoPayment(p)}, nil
}

func (s *paymentService) ListPayments(ctx context.Context, req *walletv1.ListPaymentsRequest) (*walletv1.ListPaymentsResponse, error) {
	in := payment.ListInput{AccountID: req.GetAccountId(), PageSize: int(req.GetPageSize()), PageToken: req.GetPageToken()}
	for _, st := range req.GetStatuses() {
		if st != walletv1.PaymentStatus_PAYMENT_STATUS_UNSPECIFIED {
			in.Statuses = append(in.Statuses, fromProtoStatus(st))
		}
	}
	page, err := s.payments.List(ctx, in)
	if err != nil {
		return nil, paymentError("list payments", err)
	}
	res := &walletv1.ListPaymentsResponse{NextPageToken: page.NextToken}
	for _, p := range page.Payments {
		res.Payments = append(res.Payments, toProtoPayment(p))
	}
	return res, nil
}

func (s *paymentService) ReturnPayment(ctx context.Context, req *walletv1.ReturnPaymentRequest) (*walletv1.ReturnPaymentResponse, error) {
	p, err := s.payments.Return(ctx, payment.ReturnInput{
		PaymentID: req.GetPaymentId(), AmountCents: req.GetAmountCents(), RequestID: req.GetRequestId(),
	})
	if err != nil {
		return nil, paymentError("return payment", err)
	}
	return &walletv1.ReturnPaymentResponse{Payment: toProtoPayment(p)}, nil
}

func paymentError(op string, err error) error {
	switch {
	case errors.Is(err, payment.ErrInvalid):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, ledger.ErrAccountNotFound):
		return status.Error(codes.NotFound, "account not found")
	case errors.Is(err, payment.ErrKeyNotFound):
		return status.Error(codes.NotFound, "the key is not registered")
	case errors.Is(err, payment.ErrResolutionNotFound):
		return status.Error(codes.NotFound, "the resolution is unknown or has expired; resolve the key again")
	case errors.Is(err, payment.ErrPaymentNotFound):
		return status.Error(codes.NotFound, "payment not found")
	case errors.Is(err, payment.ErrResolutionConflict):
		return status.Error(codes.AlreadyExists, "the resolution was already used with another amount or message")
	case errors.Is(err, payment.ErrRequestConflict):
		return status.Error(codes.AlreadyExists, "request_id was already used with other data")
	case errors.Is(err, payment.ErrNotReturnable), errors.Is(err, payment.ErrReturnTooLarge):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, ledger.ErrInsufficientFunds):
		return status.Error(codes.FailedPrecondition, "insufficient funds")
	case errors.Is(err, ledger.ErrAccountNotActive):
		return status.Error(codes.FailedPrecondition, "the account is blocked or closed")
	case errors.Is(err, pix.ErrRateLimited):
		return status.Error(codes.ResourceExhausted, "the DICT is limiting requests, try again later")
	case errors.Is(err, pix.ErrUnavailable):
		return status.Error(codes.Unavailable, "the DICT is unavailable, try again later")
	}
	slog.Error(op, "err", err)
	return status.Error(codes.Internal, "could not complete the request")
}

func toProtoPayee(p payment.Payee) *walletv1.Payee {
	return &walletv1.Payee{
		Name: p.Name, TaxId: p.TaxID, Participant: p.Participant, Branch: p.Branch,
		AccountNumber: p.AccountNumber, AccountType: string(p.AccountType), Key: p.Key,
	}
}

var (
	directions = map[payment.Direction]walletv1.PaymentDirection{
		payment.Outbound: walletv1.PaymentDirection_PAYMENT_DIRECTION_OUTBOUND,
		payment.Inbound:  walletv1.PaymentDirection_PAYMENT_DIRECTION_INBOUND,
	}
	types = map[payment.Type]walletv1.PaymentType{
		payment.TypePix:          walletv1.PaymentType_PAYMENT_TYPE_PIX,
		payment.TypeBookTransfer: walletv1.PaymentType_PAYMENT_TYPE_BOOK_TRANSFER,
		payment.TypeReturn:       walletv1.PaymentType_PAYMENT_TYPE_RETURN,
	}
	statuses = map[payment.Status]walletv1.PaymentStatus{
		payment.Pending:  walletv1.PaymentStatus_PAYMENT_STATUS_PENDING,
		payment.Settled:  walletv1.PaymentStatus_PAYMENT_STATUS_SETTLED,
		payment.Rejected: walletv1.PaymentStatus_PAYMENT_STATUS_REJECTED,
	}
)

func fromProtoStatus(s walletv1.PaymentStatus) payment.Status {
	for d, p := range statuses {
		if p == s {
			return d
		}
	}
	return "" // matches nothing
}

func toProtoPayment(p payment.Payment) *walletv1.Payment {
	out := &walletv1.Payment{
		Id: p.ID, AccountId: p.AccountID, Direction: directions[p.Direction], Type: types[p.Type], Status: statuses[p.Status],
		AmountCents: p.AmountCents, EndToEndId: p.EndToEndID, Counterparty: toProtoPayee(p.Counterparty), Message: p.Message,
		RejectCode: p.RejectCode, RejectReason: p.RejectReason, CreatedAt: timestamppb.New(p.CreatedAt),
		OriginalPaymentId: p.OriginalPaymentID, ReturnedCents: p.ReturnedCents,
	}
	if !p.FinishedAt.IsZero() {
		out.FinishedAt = timestamppb.New(p.FinishedAt)
	}
	return out
}
