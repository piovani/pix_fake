package dict

import "testing"

// The examples of the spec's Reconciliation section.
func TestCIDSpecExample(t *testing.T) {
	e := Entry{
		Key: "+5511987654321", KeyType: KeyPhone,
		Owner:   Owner{TaxID: "11122233300", Name: "João Silva"},
		Account: Account{Participant: "12345678", Branch: "00001", AccountNumber: "0007654321", Type: AccountChecking},
	}
	got := cidOf(e, []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})
	if want := "28c06eb41c4dc9c3ae114831efcac7446c8747777fca8b145ecd31ff8480ae88"; got != want {
		t.Fatalf("cid = %s, want %s", got, want)
	}
	viaUUID, err := CID(e, "01020304-0506-0708-090a-0b0c0d0e0f10")
	if err != nil || viaUUID != got {
		t.Fatalf("from the RequestId: %s, %v", viaUUID, err)
	}
	if _, err := CID(e, "not-a-uuid"); err == nil {
		t.Fatal("a request id that is not a UUID was accepted")
	}
}

func TestVSyncSpecExample(t *testing.T) {
	var v VSync
	if v.String() != ZeroVSync {
		t.Fatalf("empty = %s", v)
	}
	for _, cid := range []string{
		"28c06eb41c4dc9c3ae114831efcac7446c8747777fca8b145ecd31ff8480ae88",
		"4d4abb9168114e349672b934d16ed201a919cb49e28b7f66a240e62c92ee007f",
		"fce514f84f37934bc8aa0f861e4f7392273d71b9d18e8209d21e4192a7842058",
	} {
		if err := v.Add(cid); err != nil {
			t.Fatal(err)
		}
	}
	if want := "996fc1dd3b6b14bcf0c9fe8320eb66d7e2a3fd874ccf767b2e939641b1ea8eaf"; v.String() != want {
		t.Fatalf("vsync = %s, want %s", v, want)
	}
	_ = v.Add("fce514f84f37934bc8aa0f861e4f7392273d71b9d18e8209d21e4192a7842058")
	_ = v.Add("4d4abb9168114e349672b934d16ed201a919cb49e28b7f66a240e62c92ee007f")
	if v.String() != "28c06eb41c4dc9c3ae114831efcac7446c8747777fca8b145ecd31ff8480ae88" {
		t.Fatalf("removing two leaves the first: %s", v)
	}
	if err := v.Add("xyz"); err == nil {
		t.Fatal("not a CID was accepted")
	}
}
