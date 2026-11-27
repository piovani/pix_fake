// Package e2e tests the wallet through its gRPC API, with the real wallet database and the DICT mock behind it. It needs
// the environment of `make up` and a running API: `make e2e` at the repository root does both.
//
//	WALLET_E2E_GRPC_ADDR  the wallet API, such as localhost:9090
//	WALLET_E2E_DICT_URL   the DICT, such as http://localhost:8080/api/v2
//	WALLET_E2E_SPI_URL    the SPI, such as http://localhost:8081/api/v1 (optional: without it the test that makes the SPI fail is skipped)
//	WALLET_E2E_BIN        where the built cmd/accounts, cmd/reports, cmd/keys and cmd/med are, as wallet-accounts, wallet-reports, wallet-keys and wallet-med (optional)
//	WALLET_E2E_DATABASE_URL  the wallet's database, for those commands and to read report statuses (optional)
//
// The wallet's worker must be running too: it settles the payments.
package e2e

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	walletv1 "pixfake/wallet/gen/wallet/v1"
	"pixfake/wallet/internal/pix"
	"pixfake/wallet/internal/seed"
)

const (
	bankAlfa   = "99000002"
	walletISPB = "99000001"
	anaCPF     = "52998224725"
	brunoCPF   = "11144477735"
)

type env struct {
	keys     walletv1.KeyServiceClient
	payments walletv1.PaymentServiceClient
	accounts walletv1.AccountServiceClient
	spi      string          // the SPI mock's control API, empty when not given
	bank     *pix.DictClient // Banco Alfa looking keys up, the way a payer's bank does
	wallet   *pix.DictClient // the wallet's own participant, to change the DICT behind the wallet's back
	control  string          // the DICT mock's control API
}

func setup(t *testing.T) env {
	t.Helper()
	addr, dictURL := os.Getenv("WALLET_E2E_GRPC_ADDR"), os.Getenv("WALLET_E2E_DICT_URL")
	if addr == "" || dictURL == "" {
		t.Skip("WALLET_E2E_GRPC_ADDR and WALLET_E2E_DICT_URL are not set; run `make e2e` at the repository root")
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	e := env{
		keys:     walletv1.NewKeyServiceClient(conn),
		payments: walletv1.NewPaymentServiceClient(conn),
		accounts: walletv1.NewAccountServiceClient(conn),
		bank:     pix.NewDictClient(dictURL, bankAlfa),
		wallet:   pix.NewDictClient(dictURL, walletISPB),
		control:  strings.TrimSuffix(dictURL, "/api/v2"),
		spi:      strings.TrimSuffix(os.Getenv("WALLET_E2E_SPI_URL"), "/api/v1"),
	}
	e.clearFaults(t)
	t.Cleanup(func() { e.clearFaults(t) })
	return e
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	return c
}

func requestID(t *testing.T) string {
	t.Helper()
	id, err := pix.NewRequestID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func uniqueEmail(t *testing.T) string {
	return fmt.Sprintf("e2e.%s@example.com", strings.ReplaceAll(requestID(t), "-", "")[:16])
}

// lookup finds a key as another bank would before paying it.
func (e env) lookup(t *testing.T, key string) (pix.Entry, error) {
	t.Helper()
	e2eID, err := pix.NewEndToEndID(bankAlfa, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return e.bank.GetEntry(ctx(t), pix.GetEntryParams{Key: key, PayerID: "39053344705", EndToEndID: e2eID})
}

func (e env) register(t *testing.T, account string, typ walletv1.KeyType, key, reqID string) (*walletv1.Key, error) {
	t.Helper()
	res, err := e.keys.RegisterKey(ctx(t), &walletv1.RegisterKeyRequest{AccountId: account, Type: typ, Key: key, RequestId: reqID})
	return res.GetKey(), err
}

func (e env) list(t *testing.T, account string) []string {
	t.Helper()
	res, err := e.keys.ListKeys(ctx(t), &walletv1.ListKeysRequest{AccountId: account})
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	var out []string
	for _, k := range res.Keys {
		out = append(out, k.Key)
	}
	return out
}

func (e env) remove(t *testing.T, account, key string) error {
	t.Helper()
	_, err := e.keys.RemoveKey(ctx(t), &walletv1.RemoveKeyRequest{AccountId: account, Key: key})
	return err
}

func (e env) cleanup(t *testing.T, account, key string) {
	t.Cleanup(func() { _ = e.remove(t, account, key) })
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func wantCode(t *testing.T, what string, err error, want codes.Code) {
	t.Helper()
	if status.Code(err) != want {
		t.Errorf("%s: want %v, got %v", what, want, err)
	}
}

func (e env) failNext(t *testing.T, method, pathPrefix string, statusCode int) {
	t.Helper()
	body := fmt.Sprintf(`{"method":%q,"path_prefix":%q,"status":%d,"retry_after":1,"count":1}`, method, pathPrefix, statusCode)
	resp, err := http.Post(e.control+"/mock/faults", "application/json", strings.NewReader(body))
	if err != nil || resp.StatusCode >= 300 {
		t.Fatalf("could not add the fault: %v, %v", resp, err)
	}
	resp.Body.Close()
}

func (e env) clearFaults(t *testing.T) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, e.control+"/mock/faults", nil)
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
	}
}

func TestKeyLifecycle(t *testing.T) {
	e := setup(t)
	email, req := uniqueEmail(t), requestID(t)
	e.cleanup(t, seed.AnaID, email)

	k, err := e.register(t, seed.AnaID, walletv1.KeyType_KEY_TYPE_EMAIL, email, req)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if k.Key != email || k.Type != walletv1.KeyType_KEY_TYPE_EMAIL || k.AccountId != seed.AnaID || k.CreatedAt == nil {
		t.Fatalf("key: %v", k)
	}

	entry, err := e.lookup(t, email)
	if err != nil {
		t.Fatalf("lookup by another bank: %v", err)
	}
	if entry.Account.Participant != "99000001" || entry.Account.AccountNumber != "00123456" || entry.Account.Branch != "0001" ||
		entry.Account.AccountType != pix.AccountChecking || entry.Owner.Name != "Ana Souza" || entry.Owner.TaxIDNumber != anaCPF {
		t.Fatalf("entry in the DICT: %+v", entry)
	}

	if !contains(e.list(t, seed.AnaID), email) {
		t.Fatal("ListKeys does not show the new key")
	}
	if contains(e.list(t, seed.BrunoID), email) {
		t.Fatal("the key shows on another account")
	}

	again, err := e.register(t, seed.AnaID, walletv1.KeyType_KEY_TYPE_EMAIL, email, req)
	if err != nil || again.Key != email {
		t.Fatalf("repeat: %v, %v", again, err)
	}
	count := 0
	for _, key := range e.list(t, seed.AnaID) {
		if key == email {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("the key is listed %d times", count)
	}

	_, err = e.register(t, seed.AnaID, walletv1.KeyType_KEY_TYPE_EMAIL, uniqueEmail(t), req)
	wantCode(t, "same request id, other key", err, codes.AlreadyExists)
	_, err = e.register(t, seed.BrunoID, walletv1.KeyType_KEY_TYPE_EMAIL, email, requestID(t))
	wantCode(t, "key already registered", err, codes.AlreadyExists)

	if err := e.remove(t, seed.AnaID, email); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := e.lookup(t, email); !errors.Is(err, pix.ErrNotFound) {
		t.Fatalf("the key is still in the DICT: %v", err)
	}
	if contains(e.list(t, seed.AnaID), email) {
		t.Fatal("ListKeys still shows the removed key")
	}
	if err := e.remove(t, seed.AnaID, email); err != nil {
		t.Fatalf("removing again must not fail: %v", err)
	}
}

func TestRandomKey(t *testing.T) {
	e := setup(t)

	k, err := e.register(t, seed.BrunoID, walletv1.KeyType_KEY_TYPE_EVP, "", requestID(t))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	e.cleanup(t, seed.BrunoID, k.Key)

	if len(k.Key) != 36 || k.Type != walletv1.KeyType_KEY_TYPE_EVP {
		t.Fatalf("key: %v", k)
	}
	entry, err := e.lookup(t, k.Key)
	if err != nil || entry.Account.AccountNumber != "00234567" || entry.Account.AccountType != pix.AccountSavings || entry.Owner.TaxIDNumber != brunoCPF {
		t.Fatalf("entry in the DICT: %+v, %v", entry, err)
	}
}

func TestRegistrationRefusals(t *testing.T) {
	e := setup(t)

	_, err := e.register(t, seed.AnaID, walletv1.KeyType_KEY_TYPE_CPF, brunoCPF, requestID(t))
	wantCode(t, "a CPF key that is not the holder's", err, codes.InvalidArgument)

	_, err = e.register(t, seed.AnaID, walletv1.KeyType_KEY_TYPE_CPF, anaCPF, requestID(t))
	wantCode(t, "the holder's CPF, already registered in the DICT", err, codes.AlreadyExists)

	_, err = e.register(t, seed.AnaID, walletv1.KeyType_KEY_TYPE_EVP, "some-key", requestID(t))
	wantCode(t, "a random key with a key", err, codes.InvalidArgument)

	_, err = e.register(t, seed.AnaID, walletv1.KeyType_KEY_TYPE_EMAIL, uniqueEmail(t), "not-a-uuid")
	wantCode(t, "a bad request id", err, codes.InvalidArgument)

	_, err = e.register(t, seed.AnaID, walletv1.KeyType_KEY_TYPE_UNSPECIFIED, "x", requestID(t))
	wantCode(t, "no key type", err, codes.InvalidArgument)

	_, err = e.register(t, "5c1f3a52-6b7d-4e0a-9c11-2d5a7b3e9f01", walletv1.KeyType_KEY_TYPE_EMAIL, uniqueEmail(t), requestID(t))
	wantCode(t, "an unknown account", err, codes.NotFound)

	_, err = e.keys.ListKeys(ctx(t), &walletv1.ListKeysRequest{AccountId: "5c1f3a52-6b7d-4e0a-9c11-2d5a7b3e9f01"})
	wantCode(t, "listing an unknown account", err, codes.NotFound)
}

func TestRemoveOtherAccountKey(t *testing.T) {
	e := setup(t)
	email := uniqueEmail(t)
	if _, err := e.register(t, seed.AnaID, walletv1.KeyType_KEY_TYPE_EMAIL, email, requestID(t)); err != nil {
		t.Fatal(err)
	}
	e.cleanup(t, seed.AnaID, email)

	wantCode(t, "Bruno removing Ana's key", e.remove(t, seed.BrunoID, email), codes.NotFound)
	if _, err := e.lookup(t, email); err != nil {
		t.Fatalf("the key must still be in the DICT: %v", err)
	}
}

func TestRegisterDICTDown(t *testing.T) {
	e := setup(t)

	for name, tt := range map[string]struct {
		dictStatus int
		want       codes.Code
	}{
		"unavailable":  {http.StatusServiceUnavailable, codes.Unavailable},
		"rate limited": {http.StatusTooManyRequests, codes.ResourceExhausted},
	} {
		email, req := uniqueEmail(t), requestID(t)
		e.cleanup(t, seed.AnaID, email)

		e.failNext(t, http.MethodPost, "/api/v2/entries", tt.dictStatus)
		_, err := e.register(t, seed.AnaID, walletv1.KeyType_KEY_TYPE_EMAIL, email, req)
		wantCode(t, name, err, tt.want)
		if contains(e.list(t, seed.AnaID), email) {
			t.Fatalf("%s: a key that was not registered is listed", name)
		}

		k, err := e.register(t, seed.AnaID, walletv1.KeyType_KEY_TYPE_EMAIL, email, req)
		if err != nil || k.Key != email {
			t.Fatalf("%s: retry: %v, %v", name, k, err)
		}
		if _, err := e.lookup(t, email); err != nil {
			t.Fatalf("%s: the key is not in the DICT: %v", name, err)
		}
	}
}

func TestRemoveDICTDown(t *testing.T) {
	e := setup(t)
	email := uniqueEmail(t)
	if _, err := e.register(t, seed.AnaID, walletv1.KeyType_KEY_TYPE_EMAIL, email, requestID(t)); err != nil {
		t.Fatal(err)
	}
	e.cleanup(t, seed.AnaID, email)

	e.failNext(t, http.MethodPost, "/api/v2/entries/", http.StatusServiceUnavailable)
	wantCode(t, "remove while the DICT is down", e.remove(t, seed.AnaID, email), codes.Unavailable)
	if !contains(e.list(t, seed.AnaID), email) {
		t.Fatal("the key left the list although the DICT still has it")
	}
	if _, err := e.lookup(t, email); err != nil {
		t.Fatalf("the key must still be in the DICT: %v", err)
	}

	if err := e.remove(t, seed.AnaID, email); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if contains(e.list(t, seed.AnaID), email) {
		t.Fatal("the key is still listed after the retry")
	}
}

func TestRemoveKeyMissingInDICT(t *testing.T) {
	e := setup(t)
	email := uniqueEmail(t)
	if _, err := e.register(t, seed.AnaID, walletv1.KeyType_KEY_TYPE_EMAIL, email, requestID(t)); err != nil {
		t.Fatal(err)
	}

	// the DICT forgets the key (the mock was reset, say) while the wallet still has its copy
	deleteInDICT(t, e, email)

	if err := e.remove(t, seed.AnaID, email); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if contains(e.list(t, seed.AnaID), email) {
		t.Fatal("the key is still listed")
	}
}

func deleteInDICT(t *testing.T, e env, key string) {
	t.Helper()
	wallet := pix.NewDictClient(os.Getenv("WALLET_E2E_DICT_URL"), "99000001")
	if err := wallet.DeleteEntry(ctx(t), key, pix.ReasonUserRequested); err != nil {
		t.Fatalf("delete in the DICT: %v", err)
	}
}
