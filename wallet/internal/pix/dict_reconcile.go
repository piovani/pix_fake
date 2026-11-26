package pix

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// maxCIDFileSize bounds the download of a CID file: 65 bytes a key, so about a million keys of one type.
const maxCIDFileSize = 64 << 20

type SyncResult string

const (
	SyncOK  SyncResult = "OK"
	SyncNOK SyncResult = "NOK"
)

type FileStatus string

const (
	FileRequested FileStatus = "REQUESTED"
	FileAvailable FileStatus = "AVAILABLE"
)

type CIDFile struct {
	ID     int64
	Status FileStatus
	URL    string // when AVAILABLE
	Sha256 string // when AVAILABLE
}

type syncVerificationXML struct {
	Participant             string `xml:"Participant"`
	KeyType                 string `xml:"KeyType"`
	ParticipantSyncVerifier string `xml:"ParticipantSyncVerifier"`
	ID                      int64  `xml:"Id,omitempty"`
	Result                  string `xml:"Result,omitempty"`
}

type createSyncVerificationRequestXML struct {
	XMLName          xml.Name            `xml:"CreateSyncVerificationRequest"`
	SyncVerification syncVerificationXML `xml:"SyncVerification"`
}

type syncVerificationResponseXML struct {
	SyncVerification syncVerificationXML `xml:"SyncVerification"`
}

type createCIDFileRequestXML struct {
	XMLName     xml.Name `xml:"CreateCidSetFileRequest"`
	Participant string   `xml:"Participant"`
	KeyType     string   `xml:"KeyType"`
}

type cidFileResponseXML struct {
	CidSetFile struct {
		ID     int64  `xml:"Id"`
		Status string `xml:"Status"`
		URL    string `xml:"Url"`
		Sha256 string `xml:"Sha256"`
	} `xml:"CidSetFile"`
}

func (r cidFileResponseXML) toFile() CIDFile {
	f := r.CidSetFile
	return CIDFile{ID: f.ID, Status: FileStatus(f.Status), URL: f.URL, Sha256: f.Sha256}
}

type entryByCIDResponseXML struct {
	Cid       string   `xml:"Cid"`
	Entry     entryXML `xml:"Entry"`
	RequestID string   `xml:"RequestId"`
}

// CreateSyncVerification sends the wallet's VSync of one key type; OK means the DICT holds the same set of CIDs.
func (c *DictClient) CreateSyncVerification(ctx context.Context, kt KeyType, vsync string) (SyncResult, error) {
	body := createSyncVerificationRequestXML{SyncVerification: syncVerificationXML{
		Participant: c.ispb, KeyType: string(kt), ParticipantSyncVerifier: vsync,
	}}
	var out syncVerificationResponseXML
	if err := c.do(ctx, http.MethodPost, "/sync-verifications", nil, body, http.StatusCreated, &out); err != nil {
		return "", err
	}
	return SyncResult(out.SyncVerification.Result), nil
}

// CreateCIDFile asks for the file of the wallet's CIDs of one key type. The DICT makes it asynchronously: poll GetCIDFile.
func (c *DictClient) CreateCIDFile(ctx context.Context, kt KeyType) (CIDFile, error) {
	var out cidFileResponseXML
	body := createCIDFileRequestXML{Participant: c.ispb, KeyType: string(kt)}
	if err := c.do(ctx, http.MethodPost, "/cids/files", nil, body, http.StatusCreated, &out); err != nil {
		return CIDFile{}, err
	}
	return out.toFile(), nil
}

func (c *DictClient) GetCIDFile(ctx context.Context, id int64) (CIDFile, error) {
	h := http.Header{}
	h.Set("PI-RequestingParticipant", c.ispb)
	var out cidFileResponseXML
	if err := c.do(ctx, http.MethodGet, "/cids/files/"+strconv.FormatInt(id, 10), h, nil, http.StatusOK, &out); err != nil {
		return CIDFile{}, err
	}
	return out.toFile(), nil
}

// WaitCIDFile polls a file until the DICT has made it, every interval, until ctx ends.
func (c *DictClient) WaitCIDFile(ctx context.Context, id int64, interval time.Duration) (CIDFile, error) {
	for {
		f, err := c.GetCIDFile(ctx, id)
		if err != nil || f.Status == FileAvailable {
			return f, err
		}
		select {
		case <-ctx.Done():
			return CIDFile{}, fmt.Errorf("%w: CID file %d not ready: %w", ErrUnavailable, id, ctx.Err())
		case <-time.After(interval):
		}
	}
}

// DownloadCIDFile reads an available file, one CID per line, and checks it against the Sha256 the DICT gave.
func (c *DictClient) DownloadCIDFile(ctx context.Context, f CIDFile) ([]string, error) {
	if f.Status != FileAvailable || f.URL == "" {
		return nil, fmt.Errorf("pix: CID file %d is not available", f.ID)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("pix: CID file %d: %w", f.ID, err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: CID file %d: %w", ErrUnavailable, f.ID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: CID file %d: status %d", ErrUnavailable, f.ID, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxCIDFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("%w: CID file %d: %w", ErrUnavailable, f.ID, err)
	}
	if len(raw) > maxCIDFileSize {
		return nil, fmt.Errorf("pix: CID file %d is larger than %d bytes", f.ID, maxCIDFileSize)
	}
	if sum := sha256.Sum256(raw); hex.EncodeToString(sum[:]) != f.Sha256 {
		return nil, fmt.Errorf("pix: CID file %d does not match its Sha256", f.ID)
	}
	var cids []string
	for sc := bufio.NewScanner(bytes.NewReader(raw)); sc.Scan(); {
		if line := sc.Text(); line != "" {
			cids = append(cids, line)
		}
	}
	return cids, nil
}

// GetEntryByCID reads one of the wallet's entries by its CID, with the RequestId that created it.
func (c *DictClient) GetEntryByCID(ctx context.Context, cid string) (Entry, string, error) {
	var out entryByCIDResponseXML
	if err := c.do(ctx, http.MethodGet, "/cids/entries/"+cid, nil, nil, http.StatusOK, &out); err != nil {
		return Entry{}, "", err
	}
	return out.Entry.toEntry(), out.RequestID, nil
}
