package cemumegadownloader

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed titles.json
var EmbeddedTitlesJSON []byte

var WiiUCommonKey = []byte{
	0xd7, 0xb0, 0x04, 0x02, 0x65, 0x9b, 0xa2, 0xab,
	0xd2, 0xcb, 0x0d, 0xb2, 0x7f, 0xa2, 0xb6, 0x56,
}

const (
	WiiUCommonKeySHA1      = "6a0b87fc98b306ae3366f0e0a88d0b06a2813313"
	osv10TID               = "000500101000400a"
	h3TotalBlock           = 0x10000
	h3HeaderSize           = 0x400
	h3BatchSize            = h3TotalBlock * 32
	linearChunkSize        = 4 * 1024 * 1024
	maxRetries             = 5
	retryDelay             = 3 * time.Second
	maxConcurrentDownloads = 4
	RemoteMLCTitlesURL     = "https://raw.githubusercontent.com/Xpl0itU/MLCRestorerDownloader/refs/heads/main/titles.json"
	wiiuBrewDBURL          = "https://wiiubrew.org/wiki/Title_database"
)

var (
	defaultCertCache []byte
	certCacheLock    sync.Mutex

	reHTMLRow  = regexp.MustCompile(`(?is)<tr[^>]*>(.*?)</tr>`)
	reHTMLCell = regexp.MustCompile(`(?is)<td[^>]*>(.*?)</td>`)
	reHTMLTag  = regexp.MustCompile(`(?is)<[^>]+>`)
	reHTMLBr   = regexp.MustCompile(`(?is)<br\s*/?>`)
	reBrewTID  = regexp.MustCompile(`(0005[0-9a-fA-F]{4}-?[0-9a-fA-F]{8})`)
)

type TitleDefinition struct {
	TitleID string `json:"titleID"`
	Name    string `json:"name"`
}

type TitlesCatalog struct {
	EUR []TitleDefinition `json:"EUR"`
	USA []TitleDefinition `json:"USA"`
	JPN []TitleDefinition `json:"JPN"`
	All []TitleDefinition `json:"All"`
}

type Content struct {
	ID    uint32
	Index uint16
	Type  uint16
	Size  uint64
	Hash  []byte
}

type TMD struct {
	TitleID      uint64
	Version      byte
	TitleVersion uint16
	ContentCount uint16
	Contents     []Content
	Certificate1 []byte
	Certificate2 []byte
}

func NormalizeTID(titleID string) string {
	r := strings.NewReplacer("-", "", "/", "", "\\", "")
	return strings.ToLower(strings.TrimSpace(r.Replace(titleID)))
}

func fetchHTTPText(client *http.Client, urlStr string, timeout time.Duration) (string, error) {
	req, err := http.NewRequest("GET", urlStr, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	c := *client
	c.Timeout = timeout
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func loadEmbeddedNameFallback() map[string]string {
	names := make(map[string]string)
	var cat TitlesCatalog
	if json.Unmarshal(EmbeddedTitlesJSON, &cat) == nil {
		for _, list := range [][]TitleDefinition{cat.EUR, cat.USA, cat.JPN, cat.All} {
			for _, item := range list {
				norm := NormalizeTID(item.TitleID)
				if norm != "" && item.Name != "" && item.Name != norm {
					names[norm] = item.Name
				}
			}
		}
	}
	return names
}

func scrapeWiiUBrewNames(client *http.Client) map[string]string {
	names := make(map[string]string)
	body, err := fetchHTTPText(client, wiiuBrewDBURL, 15*time.Second)
	if err != nil {
		return names
	}

	rows := reHTMLRow.FindAllStringSubmatch(body, -1)
	for _, r := range rows {
		if len(r) < 2 {
			continue
		}
		cells := reHTMLCell.FindAllStringSubmatch(r[1], -1)
		if len(cells) < 2 {
			continue
		}

		c0 := strings.TrimSpace(reHTMLTag.ReplaceAllString(cells[0][1], ""))
		tidMatch := reBrewTID.FindStringSubmatch(c0)
		if len(tidMatch) < 2 {
			continue
		}
		tid := NormalizeTID(tidMatch[1])

		c1 := reHTMLBr.ReplaceAllString(cells[1][1], "\n")
		c1 = reHTMLTag.ReplaceAllString(c1, "")
		c1 = strings.TrimSpace(html.UnescapeString(c1))
		desc := strings.TrimSpace(strings.Split(c1, "\n")[0])
		cleanDesc := strings.Trim(desc, "'\"`")

		var resolvedName string
		if strings.HasPrefix(desc, "'") && strings.HasSuffix(desc, "'") && len(cells) > 2 {
			note := reHTMLBr.ReplaceAllString(cells[2][1], "\n")
			note = reHTMLTag.ReplaceAllString(note, "")
			note = strings.TrimSpace(html.UnescapeString(note))
			noteLine := strings.TrimSpace(strings.Split(note, "\n")[0])

			if len(noteLine) > 0 && len(noteLine) <= 45 &&
				!strings.HasPrefix(noteLine, "Contains ") &&
				!strings.HasPrefix(noteLine, "v") &&
				!strings.HasSuffix(noteLine, ".") &&
				!strings.EqualFold(noteLine, "TODO") &&
				!strings.EqualFold(noteLine, "N/A") {
				resolvedName = noteLine
			} else {
				resolvedName = cleanDesc
			}
		} else if cleanDesc != "" && !strings.EqualFold(cleanDesc, "N/A") {
			resolvedName = cleanDesc
		}

		if resolvedName != "" {
			names[tid] = resolvedName
		}
	}
	return names
}

func GenerateTitlesJSON(client *http.Client, dstPath string) ([]byte, error) {
	nameMap := loadEmbeddedNameFallback()

	var wg sync.WaitGroup
	var wMap map[string]string
	var rawMLC string

	wg.Add(2)
	go func() {
		defer wg.Done()
		rawMLC, _ = fetchHTTPText(client, RemoteMLCTitlesURL, 12*time.Second)
	}()
	go func() {
		defer wg.Done()
		wMap = scrapeWiiUBrewNames(client)
	}()
	wg.Wait()

	for k, v := range wMap {
		if v != "" {
			nameMap[k] = v
		}
	}

	var parsedMLC struct {
		MLC map[string][]string `json:"MLC"`
	}

	if rawMLC != "" && json.Unmarshal([]byte(rawMLC), &parsedMLC) == nil && len(parsedMLC.MLC) > 0 {
		catalog := TitlesCatalog{
			EUR: []TitleDefinition{},
			USA: []TitleDefinition{},
			JPN: []TitleDefinition{},
			All: []TitleDefinition{},
		}
		buildRegion := func(reg string) []TitleDefinition {
			seen := make(map[string]struct{})
			var out []TitleDefinition
			for _, tid := range parsedMLC.MLC[reg] {
				norm := NormalizeTID(tid)
				if norm == "" || norm == "dummy" {
					continue
				}
				if _, exists := seen[norm]; !exists {
					seen[norm] = struct{}{}
					titleName := nameMap[norm]
					if titleName == "" {
						titleName = norm
					}
					out = append(out, TitleDefinition{
						TitleID: norm,
						Name:    titleName,
					})
				}
			}
			return out
		}
		catalog.EUR = buildRegion("EUR")
		catalog.USA = buildRegion("USA")
		catalog.JPN = buildRegion("JPN")
		catalog.All = buildRegion("All")

		outBytes, err := json.MarshalIndent(catalog, "", "  ")
		if err == nil {
			if dstPath != "" {
				_ = os.WriteFile(dstPath, outBytes, 0644)
			}
			return outBytes, nil
		}
	}

	if len(EmbeddedTitlesJSON) > 0 {
		if dstPath != "" {
			_ = os.WriteFile(dstPath, EmbeddedTitlesJSON, 0644)
		}
		return EmbeddedTitlesJSON, nil
	}

	return nil, errors.New("failed to generate titles.json")
}

func ParseTMD(data []byte) (*TMD, error) {
	if len(data) < 0x200 {
		return nil, fmt.Errorf("TMD file too small: %d bytes", len(data))
	}

	tmd := &TMD{}
	reader := bytes.NewReader(data)

	reader.Seek(0x180, io.SeekStart)
	if err := binary.Read(reader, binary.BigEndian, &tmd.Version); err != nil {
		return nil, err
	}

	switch tmd.Version {
	case 0x00:
		reader.Seek(0x18C, io.SeekStart)
		if err := binary.Read(reader, binary.BigEndian, &tmd.TitleID); err != nil {
			return nil, err
		}
		reader.Seek(0x1DC, io.SeekStart)
		if err := binary.Read(reader, binary.BigEndian, &tmd.TitleVersion); err != nil {
			return nil, err
		}
		if err := binary.Read(reader, binary.BigEndian, &tmd.ContentCount); err != nil {
			return nil, err
		}

		minNeeded := uint64(0x1E4) + (uint64(tmd.ContentCount) * 0x24)
		if uint64(len(data)) < minNeeded {
			return nil, fmt.Errorf("TMD v0 too small for %d contents (needs %d bytes, got %d)", tmd.ContentCount, minNeeded, len(data))
		}

		tmd.Contents = make([]Content, tmd.ContentCount)
		for i := 0; i < int(tmd.ContentCount); i++ {
			offset := int64(0x1E4 + (0x24 * i))
			reader.Seek(offset, io.SeekStart)
			if err := binary.Read(reader, binary.BigEndian, &tmd.Contents[i].ID); err != nil {
				return nil, err
			}
			if err := binary.Read(reader, binary.BigEndian, &tmd.Contents[i].Index); err != nil {
				return nil, err
			}
			if err := binary.Read(reader, binary.BigEndian, &tmd.Contents[i].Type); err != nil {
				return nil, err
			}
			if err := binary.Read(reader, binary.BigEndian, &tmd.Contents[i].Size); err != nil {
				return nil, err
			}
			tmd.Contents[i].Hash = make([]byte, 0x14)
			if err := binary.Read(reader, binary.BigEndian, &tmd.Contents[i].Hash); err != nil {
				return nil, err
			}
		}
		tmd.Certificate1 = make([]byte, 0x400)
		if _, err := io.ReadFull(reader, tmd.Certificate1); err != nil {
			return nil, err
		}
		tmd.Certificate2 = make([]byte, 0x300)
		if _, err := io.ReadFull(reader, tmd.Certificate2); err != nil {
			return nil, err
		}

	case 0x01:
		reader.Seek(0x18C, io.SeekStart)
		if err := binary.Read(reader, binary.BigEndian, &tmd.TitleID); err != nil {
			return nil, err
		}
		reader.Seek(0x1DC, io.SeekStart)
		if err := binary.Read(reader, binary.BigEndian, &tmd.TitleVersion); err != nil {
			return nil, err
		}
		if err := binary.Read(reader, binary.BigEndian, &tmd.ContentCount); err != nil {
			return nil, err
		}

		minNeeded := uint64(0xB04) + (uint64(tmd.ContentCount) * 0x30)
		if uint64(len(data)) < minNeeded {
			return nil, fmt.Errorf("TMD v1 too small for %d contents (needs %d bytes, got %d)", tmd.ContentCount, minNeeded, len(data))
		}

		tmd.Contents = make([]Content, tmd.ContentCount)
		for c := 0; c < int(tmd.ContentCount); c++ {
			offset := int64(0xB04 + (0x30 * c))
			reader.Seek(offset, io.SeekStart)
			if err := binary.Read(reader, binary.BigEndian, &tmd.Contents[c].ID); err != nil {
				return nil, err
			}
			if err := binary.Read(reader, binary.BigEndian, &tmd.Contents[c].Index); err != nil {
				return nil, err
			}
			if err := binary.Read(reader, binary.BigEndian, &tmd.Contents[c].Type); err != nil {
				return nil, err
			}
			if err := binary.Read(reader, binary.BigEndian, &tmd.Contents[c].Size); err != nil {
				return nil, err
			}
			reader.Seek(offset+16, io.SeekStart)
			tmd.Contents[c].Hash = make([]byte, 0x14)
			if err := binary.Read(reader, binary.BigEndian, &tmd.Contents[c].Hash); err != nil {
				return nil, err
			}
		}
		tmd.Certificate1 = make([]byte, 0x400)
		if err := binary.Read(reader, binary.BigEndian, &tmd.Certificate1); err != nil {
			return nil, err
		}
		tmd.Certificate2 = make([]byte, 0x300)
		if err := binary.Read(reader, binary.BigEndian, &tmd.Certificate2); err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("unknown TMD version: %d", tmd.Version)
	}

	return tmd, nil
}

func getDefaultRootCert(client *http.Client) ([]byte, error) {
	certCacheLock.Lock()
	defer certCacheLock.Unlock()

	if len(defaultCertCache) == 0x300 {
		return defaultCertCache, nil
	}

	url := fmt.Sprintf("http://ccs.cdn.c.shop.nintendowifi.net/ccs/download/%s/cetk", osv10TID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "WiiUDownloader")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch root certificate: HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if len(data) < 0x650 {
		return nil, fmt.Errorf("invalid cetk size: %d", len(data))
	}

	defaultCertCache = make([]byte, 0x300)
	copy(defaultCertCache, data[0x350:0x650])
	return defaultCertCache, nil
}

func generateCert(tmd *TMD, certPath string, client *http.Client) error {
	caCert, err := getDefaultRootCert(client)
	if err != nil {
		return err
	}

	fullCert := make([]byte, 0, len(tmd.Certificate1)+len(tmd.Certificate2)+len(caCert))
	fullCert = append(fullCert, tmd.Certificate1...)
	fullCert = append(fullCert, tmd.Certificate2...)
	fullCert = append(fullCert, caCert...)

	return os.WriteFile(certPath, fullCert, 0644)
}

func ReadTMDAndTitleKey(tmdPath, tikPath string, expectedTID string) (*TMD, []byte, error) {
	rawTMD, err := os.ReadFile(tmdPath)
	if err != nil {
		return nil, nil, err
	}
	if len(rawTMD) < 0x200 {
		return nil, nil, fmt.Errorf("invalid TMD size: %d", len(rawTMD))
	}

	tmd, err := ParseTMD(rawTMD)
	if err != nil {
		return nil, nil, err
	}
	if tmd.ContentCount == 0 || len(tmd.Contents) != int(tmd.ContentCount) {
		return nil, nil, errors.New("invalid TMD content count")
	}

	if expectedTID != "" {
		normExpected := NormalizeTID(expectedTID)
		expectedInt, err := strconv.ParseUint(normExpected, 16, 64)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid expected title ID %s: %w", expectedTID, err)
		}
		if tmd.TitleID != expectedInt {
			return nil, nil, fmt.Errorf("TMD title ID mismatch: got %016x, expected %016x", tmd.TitleID, expectedInt)
		}
	}

	rawTik, err := os.ReadFile(tikPath)
	if err != nil {
		return nil, nil, err
	}
	if len(rawTik) < 0x1D0 {
		return nil, nil, fmt.Errorf("invalid ticket size: %d", len(rawTik))
	}
	if len(rawTik) >= 0x1E4 {
		tikTID := binary.BigEndian.Uint64(rawTik[0x1DC:0x1E4])
		if tikTID != tmd.TitleID {
			return nil, nil, fmt.Errorf("manifest mismatch between TMD (%016x) and ticket (%016x)", tmd.TitleID, tikTID)
		}
	}

	commonBlock, err := aes.NewCipher(WiiUCommonKey)
	if err != nil {
		return nil, nil, err
	}

	var ivTKey [16]byte
	copy(ivTKey[:8], rawTMD[0x18C:0x194])

	titleKey := make([]byte, 16)
	cipher.NewCBCDecrypter(commonBlock, ivTKey[:]).CryptBlocks(titleKey, rawTik[0x1BF:0x1CF])

	return tmd, titleKey, nil
}

func resolveContentFile(workDir string, cid uint32, ext string) (string, error) {
	candidates := []string{
		filepath.Join(workDir, fmt.Sprintf("%08X%s", cid, ext)),
		filepath.Join(workDir, fmt.Sprintf("%08x%s", cid, ext)),
	}
	for _, p := range candidates {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
	}
	return candidates[0], fmt.Errorf("missing file %s", filepath.Base(candidates[0]))
}

func verifySingleContent(workDir string, c Content, titleBlock cipher.Block) error {
	appPath, err := resolveContentFile(workDir, c.ID, ".app")
	if err != nil {
		return err
	}
	appName := filepath.Base(appPath)

	if len(c.Hash) < 20 {
		return fmt.Errorf("invalid content hash length in TMD for %s", appName)
	}
	expectedHash := c.Hash[:20]

	var baseIV [16]byte
	binary.BigEndian.PutUint16(baseIV[0:2], c.Index)

	if (c.Type & 0x02) != 0 {
		h3Path, err := resolveContentFile(workDir, c.ID, ".h3")
		if err != nil {
			return err
		}
		h3Name := filepath.Base(h3Path)

		h3Raw, err := os.ReadFile(h3Path)
		if err != nil {
			return err
		}

		h3Sum := sha1.Sum(h3Raw)
		if !bytes.Equal(h3Sum[:], expectedHash) {
			return fmt.Errorf("H3 SHA-1 mismatch for %s", h3Name)
		}

		fApp, err := os.Open(appPath)
		if err != nil {
			return err
		}
		defer fApp.Close()

		idxHi := byte((c.Index >> 8) & 0xFF)
		idxLo := byte(c.Index & 0xFF)
		chunkIdx := 0

		buf := make([]byte, h3BatchSize)
		hashes := make([]byte, h3HeaderSize)
		decPayload := make([]byte, h3TotalBlock-h3HeaderSize)
		var cIV [16]byte
		var expectedTrueHash [20]byte

		for {
			nRead, readErr := io.ReadFull(fApp, buf)
			if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
				return readErr
			}
			if nRead == 0 {
				break
			}

			pos := 0
			for pos < nRead {
				blockEnd := pos + h3TotalBlock
				if blockEnd > nRead {
					blockEnd = nRead
				}
				blockLen := blockEnd - pos
				if blockLen <= h3HeaderSize {
					return fmt.Errorf("short H3 block in %s at chunk %d", appName, chunkIdx)
				}

				cipher.NewCBCDecrypter(titleBlock, baseIV[:]).CryptBlocks(hashes, buf[pos:pos+h3HeaderSize])

				blockNum := chunkIdx & 0xF
				h0Off := blockNum * 20
				copy(expectedTrueHash[:], hashes[h0Off:h0Off+20])
				copy(cIV[:], expectedTrueHash[:16])

				if blockNum == 0 {
					cIV[0] ^= idxHi
					cIV[1] ^= idxLo
					expectedTrueHash[0] ^= idxHi
					expectedTrueHash[1] ^= idxLo
				}

				payloadLen := blockLen - h3HeaderSize
				encSlice := buf[pos+h3HeaderSize : blockEnd]
				rem := payloadLen & 0xF

				bodyDec := cipher.NewCBCDecrypter(titleBlock, cIV[:])
				var validDec []byte

				if rem != 0 {
					paddedLen := payloadLen + (16 - rem)
					paddedEnc := make([]byte, paddedLen)
					copy(paddedEnc, encSlice)
					paddedDec := make([]byte, paddedLen)
					bodyDec.CryptBlocks(paddedDec, paddedEnc)
					validDec = paddedDec[:payloadLen]
				} else {
					bodyDec.CryptBlocks(decPayload[:payloadLen], encSlice)
					validDec = decPayload[:payloadLen]
				}

				payloadSum := sha1.Sum(validDec)
				if payloadSum != expectedTrueHash {
					return fmt.Errorf("H0 hash mismatch in %s at chunk %d", appName, chunkIdx)
				}

				chunkIdx++
				pos += h3TotalBlock
			}

			if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
				break
			}
		}

		return nil
	}

	stat, err := os.Stat(appPath)
	if err != nil {
		return err
	}
	actualSize := uint64(stat.Size())
	alignedSize := (c.Size + 15) & ^uint64(15)
	if actualSize != c.Size && actualSize != alignedSize {
		return fmt.Errorf("size mismatch for %s: got %d, expected %d", appName, actualSize, c.Size)
	}

	fApp, err := os.Open(appPath)
	if err != nil {
		return err
	}
	defer fApp.Close()

	decMode := cipher.NewCBCDecrypter(titleBlock, baseIV[:])
	hasher := sha1.New()

	var processedSize uint64
	buf := make([]byte, linearChunkSize)
	decBuf := make([]byte, linearChunkSize)

	for processedSize < c.Size {
		nRead, readErr := fApp.Read(buf)
		if nRead > 0 {
			rem := nRead & 0xF
			var chunkLen uint64
			var decSlice []byte

			if rem != 0 {
				paddedLen := nRead + (16 - rem)
				paddedEnc := make([]byte, paddedLen)
				copy(paddedEnc, buf[:nRead])
				paddedDec := make([]byte, paddedLen)
				decMode.CryptBlocks(paddedDec, paddedEnc)
				decSlice = paddedDec
				chunkLen = uint64(paddedLen)
			} else {
				decMode.CryptBlocks(decBuf[:nRead], buf[:nRead])
				decSlice = decBuf[:nRead]
				chunkLen = uint64(nRead)
			}

			if processedSize+uint64(len(decSlice)) >= c.Size {
				validLen := c.Size - processedSize
				hasher.Write(decSlice[:validLen])
				processedSize += validLen
				break
			}

			hasher.Write(decSlice)
			processedSize += chunkLen
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return readErr
		}
	}

	if processedSize < c.Size {
		return fmt.Errorf("unexpected EOF in %s (%d/%d bytes)", appName, processedSize, c.Size)
	}

	computed := hasher.Sum(nil)
	if !bytes.Equal(computed, expectedHash) {
		return fmt.Errorf("linear SHA-1 mismatch for %s", appName)
	}

	return nil
}

func verifyExtractedFST(outputDir string, tmd *TMD) error {
	fstPath := filepath.Join(outputDir, "code", "title.fst")
	fstDec, err := os.ReadFile(fstPath)
	if err != nil {
		return fmt.Errorf("failed to read title.fst: %w", err)
	}
	if len(fstDec) < 0x20 {
		return fmt.Errorf("title.fst too small: %d bytes", len(fstDec))
	}

	infoCount := binary.BigEndian.Uint32(fstDec[0x08:0x0C])
	entriesPtr := uint64(0x20) + (uint64(infoCount) * 0x20)
	if entriesPtr+16 > uint64(len(fstDec)) {
		return fmt.Errorf("invalid FST header: entriesPtr out of bounds")
	}

	totalEntries := binary.BigEndian.Uint32(fstDec[entriesPtr+8 : entriesPtr+12])
	nameTableOff := entriesPtr + (uint64(totalEntries) * 0x10)
	if nameTableOff > uint64(len(fstDec)) || totalEntries == 0 || totalEntries >= 100000 {
		return fmt.Errorf("invalid FST table: totalEntries (%d) or nameTableOff out of bounds", totalEntries)
	}

	contentMap := make(map[uint16]Content, len(tmd.Contents))
	for _, c := range tmd.Contents {
		contentMap[c.Index] = c
	}

	pathStack := []string{outputDir}
	endStack := []uint32{totalEntries}

	for i := uint32(1); i < totalEntries; i++ {
		for len(endStack) > 0 && i >= endStack[len(endStack)-1] {
			pathStack = pathStack[:len(pathStack)-1]
			endStack = endStack[:len(endStack)-1]
		}

		off := entriesPtr + (uint64(i) * 0x10)
		if off+16 > uint64(len(fstDec)) {
			return fmt.Errorf("FST entry %d out of bounds", i)
		}

		eType := fstDec[off]
		eNameOff := (uint32(fstDec[off+1]) << 16) | (uint32(fstDec[off+2]) << 8) | uint32(fstDec[off+3])
		eLen := binary.BigEndian.Uint32(fstDec[off+8 : off+12])
		eCID := binary.BigEndian.Uint16(fstDec[off+14 : off+16])

		nameStart := nameTableOff + uint64(eNameOff)
		if nameStart >= uint64(len(fstDec)) {
			continue
		}
		nameEnd := bytes.IndexByte(fstDec[nameStart:], 0)
		if nameEnd == -1 {
			continue
		}
		name := string(fstDec[nameStart : nameStart+uint64(nameEnd)])
		if name == "" {
			continue
		}

		currentPath := filepath.Join(pathStack[len(pathStack)-1], name)
		if (eType & 0x01) != 0 {
			pathStack = append(pathStack, currentPath)
			endStack = append(endStack, eLen)
		} else if (eType & 0x80) == 0 {
			if _, ok := contentMap[eCID]; ok {
				st, err := os.Stat(currentPath)
				if err != nil || st.IsDir() {
					return fmt.Errorf("missing extracted file %s", currentPath)
				}
				if uint32(st.Size()) != eLen {
					return fmt.Errorf("extracted file size mismatch for %s: got %d, expected %d", currentPath, st.Size(), eLen)
				}
			}
		}
	}
	return nil
}

func VerifyTitle(titleID, outputDirectory string) error {
	outputDir := strings.TrimRight(outputDirectory, "/\\")
	tmdPath := filepath.Join(outputDir, "title.tmd")
	tikPath := filepath.Join(outputDir, "title.tik")
	certPath := filepath.Join(outputDir, "title.cert")

	isExtracted := false
	if _, err := os.Stat(filepath.Join(outputDir, "code", "title.tmd")); err == nil {
		tmdPath = filepath.Join(outputDir, "code", "title.tmd")
		tikPath = filepath.Join(outputDir, "code", "title.tik")
		certPath = filepath.Join(outputDir, "code", "title.cert")
		isExtracted = true
	}

	certInfo, err := os.Stat(certPath)
	if err != nil || certInfo.IsDir() || certInfo.Size() == 0 {
		return fmt.Errorf("missing or empty title.cert for %s", titleID)
	}

	tmd, titleKey, err := ReadTMDAndTitleKey(tmdPath, tikPath, titleID)
	if err != nil {
		return fmt.Errorf("integrity check failed for %s: %w", titleID, err)
	}

	if isExtracted {
		if _, err := os.Stat(filepath.Join(outputDir, fmt.Sprintf("%08X.app", tmd.Contents[0].ID))); err != nil {
			return verifyExtractedFST(outputDir, tmd)
		}
	}

	titleBlock, err := aes.NewCipher(titleKey)
	if err != nil {
		return err
	}

	for _, c := range tmd.Contents {
		if err := verifySingleContent(outputDir, c, titleBlock); err != nil {
			return fmt.Errorf("title %s content %08X verification failed: %w", titleID, c.ID, err)
		}
	}

	return nil
}

func extractFileStandard(srcApp, dstPath string, titleBlock cipher.Block, contentIdx uint16, offset, length uint64) error {
	_ = os.Remove(dstPath)
	if length == 0 {
		return os.WriteFile(dstPath, []byte{}, 0644)
	}

	fIn, err := os.Open(srcApp)
	if err != nil {
		return err
	}
	defer fIn.Close()

	var baseIV [16]byte
	binary.BigEndian.PutUint16(baseIV[0:2], contentIdx)

	alignedOffset := offset & ^uint64(15)
	skipBytes := offset - alignedOffset

	var iv [16]byte
	if alignedOffset > 0 {
		if _, err := fIn.Seek(int64(alignedOffset-16), io.SeekStart); err != nil {
			return err
		}
		if _, err := io.ReadFull(fIn, iv[:]); err != nil {
			return err
		}
	} else {
		copy(iv[:], baseIV[:])
	}

	if _, err := fIn.Seek(int64(alignedOffset), io.SeekStart); err != nil {
		return err
	}

	fOut, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer fOut.Close()

	dec := cipher.NewCBCDecrypter(titleBlock, iv[:])
	remaining := length
	firstChunk := true

	bufSize := linearChunkSize
	encBuf := make([]byte, bufSize)
	decBuf := make([]byte, bufSize)

	for remaining > 0 {
		toRead := uint64(bufSize)
		need := ((remaining + skipBytes + 15) & ^uint64(15))
		if need < toRead {
			toRead = need
		}

		nRead, readErr := io.ReadFull(fIn, encBuf[:toRead])
		if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
			return readErr
		}
		if nRead == 0 {
			fOut.Write(make([]byte, remaining))
			break
		}

		rem := nRead & 0xF
		var chunkSlice []byte
		if rem != 0 {
			paddedLen := nRead + (16 - rem)
			padded := make([]byte, paddedLen)
			copy(padded, encBuf[:nRead])
			pDec := make([]byte, paddedLen)
			dec.CryptBlocks(pDec, padded)
			chunkSlice = pDec[:nRead]
		} else {
			dec.CryptBlocks(decBuf[:nRead], encBuf[:nRead])
			chunkSlice = decBuf[:nRead]
		}

		if firstChunk {
			if uint64(len(chunkSlice)) > skipBytes {
				chunkSlice = chunkSlice[skipBytes:]
			} else {
				chunkSlice = []byte{}
			}
			firstChunk = false
		}

		writeLen := uint64(len(chunkSlice))
		if writeLen > remaining {
			writeLen = remaining
		}

		if _, err := fOut.Write(chunkSlice[:writeLen]); err != nil {
			return err
		}
		remaining -= writeLen

		if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
			if remaining > 0 {
				fOut.Write(make([]byte, remaining))
			}
			break
		}
	}

	return nil
}

func extractFileHashed(srcApp, dstPath string, titleBlock cipher.Block, contentIdx uint16, offset, length uint64) error {
	_ = os.Remove(dstPath)
	if length == 0 {
		return os.WriteFile(dstPath, []byte{}, 0644)
	}

	fIn, err := os.Open(srcApp)
	if err != nil {
		return err
	}
	defer fIn.Close()

	fOut, err := os.Create(dstPath)
	if err != nil {
		return err
	}
	defer fOut.Close()

	const dataBlock = 0xFC00
	const totalBlock = 0x10000

	startChunkIdx := offset / dataBlock
	soffset := offset % dataBlock

	var baseIV [16]byte
	binary.BigEndian.PutUint16(baseIV[0:2], contentIdx)
	idxHi := byte((contentIdx >> 8) & 0xFF)
	idxLo := byte(contentIdx & 0xFF)

	remaining := length
	currentChunkIdx := startChunkIdx

	blockBuf := make([]byte, totalBlock)
	hashes := make([]byte, h3HeaderSize)
	decPayload := make([]byte, dataBlock)
	var cIV [16]byte

	for remaining > 0 {
		targetPos := int64(currentChunkIdx) * int64(totalBlock)
		if _, err := fIn.Seek(targetPos, io.SeekStart); err != nil {
			return err
		}

		nRead, readErr := io.ReadFull(fIn, blockBuf)
		if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
			return readErr
		}
		if nRead <= h3HeaderSize {
			fOut.Write(make([]byte, remaining))
			break
		}

		cipher.NewCBCDecrypter(titleBlock, baseIV[:]).CryptBlocks(hashes, blockBuf[:h3HeaderSize])

		blockNum := currentChunkIdx & 0xF
		h0Off := blockNum * 20
		copy(cIV[:], hashes[h0Off:h0Off+16])

		if blockNum == 0 {
			cIV[0] ^= idxHi
			cIV[1] ^= idxLo
		}

		payloadLen := nRead - h3HeaderSize
		encSlice := blockBuf[h3HeaderSize:nRead]
		rem := payloadLen & 0xF

		bodyDec := cipher.NewCBCDecrypter(titleBlock, cIV[:])
		var validDec []byte

		if rem != 0 {
			paddedLen := payloadLen + (16 - rem)
			padded := make([]byte, paddedLen)
			copy(padded, encSlice)
			pDec := make([]byte, paddedLen)
			bodyDec.CryptBlocks(pDec, padded)
			validDec = pDec[:payloadLen]
		} else {
			bodyDec.CryptBlocks(decPayload[:payloadLen], encSlice)
			validDec = decPayload[:payloadLen]
		}

		if soffset < uint64(len(validDec)) {
			avail := uint64(len(validDec)) - soffset
			writeLen := remaining
			if writeLen > avail {
				writeLen = avail
			}

			if _, err := fOut.Write(validDec[soffset : soffset+writeLen]); err != nil {
				return err
			}
			remaining -= writeLen
		} else {
			fOut.Write(make([]byte, remaining))
			break
		}

		soffset = 0
		currentChunkIdx++

		if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
			if remaining > 0 {
				fOut.Write(make([]byte, remaining))
			}
			break
		}
	}

	return nil
}

func DecryptAndExtractTitle(workDir, targetDir string) error {
	workDir = strings.TrimRight(workDir, "/\\")
	targetDir = strings.TrimRight(targetDir, "/\\")

	tmdPath := filepath.Join(workDir, "title.tmd")
	tikPath := filepath.Join(workDir, "title.tik")
	certPath := filepath.Join(workDir, "title.cert")

	tmd, titleKey, err := ReadTMDAndTitleKey(tmdPath, tikPath, "")
	if err != nil {
		return err
	}

	rawTMD, err := os.ReadFile(tmdPath)
	if err != nil {
		return err
	}
	rawTik, err := os.ReadFile(tikPath)
	if err != nil {
		return err
	}
	rawCert, _ := os.ReadFile(certPath)

	titleBlock, err := aes.NewCipher(titleKey)
	if err != nil {
		return err
	}

	var c0 *Content
	for i := range tmd.Contents {
		if tmd.Contents[i].Index == 0 {
			c0 = &tmd.Contents[i]
			break
		}
	}
	if c0 == nil {
		return errors.New("content index 0 not found in TMD")
	}

	c0Path, err := resolveContentFile(workDir, c0.ID, ".app")
	if err != nil {
		return err
	}

	c0Enc, err := os.ReadFile(c0Path)
	if err != nil {
		return err
	}

	var zeroIV [16]byte
	fstDec := make([]byte, len(c0Enc))
	cipher.NewCBCDecrypter(titleBlock, zeroIV[:]).CryptBlocks(fstDec, c0Enc)

	codeDir := filepath.Join(targetDir, "code")
	if err := os.MkdirAll(codeDir, 0755); err != nil {
		return err
	}

	_ = os.WriteFile(filepath.Join(codeDir, "title.tmd"), rawTMD, 0644)
	_ = os.WriteFile(filepath.Join(codeDir, "title.tik"), rawTik, 0644)
	if len(rawCert) > 0 {
		_ = os.WriteFile(filepath.Join(codeDir, "title.cert"), rawCert, 0644)
	}

	isFST := false
	var totalEntries uint32
	var entriesPtr, nameTableOff uint64
	var offsetFactor uint64 = 0x20

	if len(fstDec) >= 0x20 && binary.BigEndian.Uint32(fstDec[0x00:0x04]) == 0x46535400 {
		factor := binary.BigEndian.Uint32(fstDec[0x04:0x08])
		if factor != 0 {
			offsetFactor = uint64(factor)
		}

		infoCount := binary.BigEndian.Uint32(fstDec[0x08:0x0C])
		entriesPtr = uint64(0x20) + (uint64(infoCount) * 0x20)
		if entriesPtr+16 <= uint64(len(fstDec)) {
			totalEntries = binary.BigEndian.Uint32(fstDec[entriesPtr+8 : entriesPtr+12])
			nameTableOff = entriesPtr + (uint64(totalEntries) * 0x10)
			if nameTableOff <= uint64(len(fstDec)) && totalEntries > 0 && totalEntries < 100000 {
				isFST = true
			}
		}
	}

	if isFST {
		_ = os.WriteFile(filepath.Join(codeDir, "title.fst"), fstDec, 0644)

		contentMap := make(map[uint16]Content, len(tmd.Contents))
		for _, c := range tmd.Contents {
			contentMap[c.Index] = c
		}

		pathStack := []string{targetDir}
		endStack := []uint32{totalEntries}

		for i := uint32(1); i < totalEntries; i++ {
			for len(endStack) > 0 && i >= endStack[len(endStack)-1] {
				pathStack = pathStack[:len(pathStack)-1]
				endStack = endStack[:len(endStack)-1]
			}

			off := entriesPtr + (uint64(i) * 0x10)
			if off+16 > uint64(len(fstDec)) {
				continue
			}

			eType := fstDec[off]
			eNameOff := (uint32(fstDec[off+1]) << 16) | (uint32(fstDec[off+2]) << 8) | uint32(fstDec[off+3])
			eOff := binary.BigEndian.Uint32(fstDec[off+4 : off+8])
			eLen := binary.BigEndian.Uint32(fstDec[off+8 : off+12])
			eFlags := binary.BigEndian.Uint16(fstDec[off+12 : off+14])
			eCID := binary.BigEndian.Uint16(fstDec[off+14 : off+16])

			nameStart := nameTableOff + uint64(eNameOff)
			if nameStart >= uint64(len(fstDec)) {
				continue
			}
			nameEnd := bytes.IndexByte(fstDec[nameStart:], 0)
			if nameEnd == -1 {
				continue
			}
			name := string(fstDec[nameStart : nameStart+uint64(nameEnd)])
			if name == "" {
				continue
			}

			currentPath := filepath.Join(pathStack[len(pathStack)-1], name)

			if (eType & 0x01) != 0 {
				if err := os.MkdirAll(currentPath, 0755); err != nil {
					return err
				}
				pathStack = append(pathStack, currentPath)
				endStack = append(endStack, eLen)
			} else {
				if (eType & 0x80) == 0 {
					c, ok := contentMap[eCID]
					if !ok {
						continue
					}
					srcApp, err := resolveContentFile(workDir, c.ID, ".app")
					if err != nil {
						return err
					}

					var realOffset uint64
					if (eFlags & 0x04) != 0 {
						realOffset = uint64(eOff)
					} else {
						realOffset = uint64(eOff) * offsetFactor
					}

					if err := os.MkdirAll(filepath.Dir(currentPath), 0755); err != nil {
						return err
					}

					if (c.Type & 0x02) != 0 {
						if err := extractFileHashed(srcApp, currentPath, titleBlock, c.Index, realOffset, uint64(eLen)); err != nil {
							return err
						}
					} else {
						if err := extractFileStandard(srcApp, currentPath, titleBlock, c.Index, realOffset, uint64(eLen)); err != nil {
							return err
						}
					}
				}
			}
		}
	} else {
		contentDir := filepath.Join(targetDir, "content")
		if err := os.MkdirAll(contentDir, 0755); err != nil {
			return err
		}
		for _, c := range tmd.Contents {
			srcApp, err := resolveContentFile(workDir, c.ID, ".app")
			if err != nil {
				return err
			}
			dstPath := filepath.Join(contentDir, fmt.Sprintf("%08X.app", c.ID))
			if err := extractFileStandard(srcApp, dstPath, titleBlock, c.Index, 0, c.Size); err != nil {
				return err
			}
		}
	}

	return nil
}

func CleanRawNUSFiles(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			name := strings.ToLower(e.Name())
			if strings.HasSuffix(name, ".app") || strings.HasSuffix(name, ".h3") ||
				name == "title.tmd" || name == "title.tik" || name == "title.cert" {
				_ = os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
}

func GetTitleSize(titleID string, client *http.Client) (int64, error) {
	cleanTID := NormalizeTID(titleID)
	url := fmt.Sprintf("http://ccs.cdn.c.shop.nintendowifi.net/ccs/download/%s/tmd", cleanTID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "WiiUDownloader")

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	tmd, err := ParseTMD(data)
	if err != nil {
		return 0, err
	}

	var total uint64
	for i := range tmd.Contents {
		total += tmd.Contents[i].Size
	}
	return int64(total), nil
}

func downloadFile(client *http.Client, downloadURL, dstPath string, onProgress func(int64)) error {
	for attempt := 1; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequest("GET", downloadURL, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", "WiiUDownloader")

		resp, err := client.Do(req)
		if err != nil {
			if attempt < maxRetries {
				time.Sleep(retryDelay)
				continue
			}
			return err
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			if attempt < maxRetries {
				time.Sleep(retryDelay)
				continue
			}
			return fmt.Errorf("HTTP error %d for %s", resp.StatusCode, downloadURL)
		}

		file, err := os.Create(dstPath)
		if err != nil {
			resp.Body.Close()
			return err
		}

		buf := make([]byte, 128*1024)
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				if _, writeErr := file.Write(buf[:n]); writeErr != nil {
					file.Close()
					resp.Body.Close()
					return writeErr
				}
				if onProgress != nil {
					onProgress(int64(n))
				}
			}
			if readErr != nil {
				if readErr == io.EOF {
					break
				}
				file.Close()
				resp.Body.Close()
				if attempt < maxRetries {
					time.Sleep(retryDelay)
					break
				}
				return readErr
			}
		}

		file.Close()
		resp.Body.Close()
		return nil
	}

	return fmt.Errorf("failed to download %s after %d retries", downloadURL, maxRetries)
}

func DownloadAndExtractTitle(
	ctx context.Context,
	titleID, outputPath string,
	client *http.Client,
	cleanInstall bool,
	onProgress func(int64),
) error {
	targetDir := strings.TrimRight(outputPath, "/\\")
	baseURL := fmt.Sprintf("http://ccs.cdn.c.shop.nintendowifi.net/ccs/download/%s", titleID)

	if cleanInstall {
		normTarget := filepath.Clean(targetDir)
		pathParts := strings.Split(normTarget, string(filepath.Separator))
		isSysTitle := false
		for _, part := range pathParts {
			if part == "sys" {
				isSysTitle = true
				break
			}
		}
		if isSysTitle {
			_ = os.RemoveAll(targetDir)
		}
	}

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return err
	}

	tmdPath := filepath.Join(targetDir, "title.tmd")
	if err := downloadFile(client, fmt.Sprintf("%s/tmd", baseURL), tmdPath, nil); err != nil {
		return err
	}

	tmdData, err := os.ReadFile(tmdPath)
	if err != nil {
		return err
	}

	tmd, err := ParseTMD(tmdData)
	if err != nil {
		return err
	}

	tikPath := filepath.Join(targetDir, "title.tik")
	if err := downloadFile(client, fmt.Sprintf("%s/cetk", baseURL), tikPath, nil); err != nil {
		return err
	}

	if err := generateCert(tmd, filepath.Join(targetDir, "title.cert"), client); err != nil {
		return err
	}

	type dlTask struct {
		url  string
		path string
	}
	var tasks []dlTask

	for _, c := range tmd.Contents {
		tasks = append(tasks, dlTask{
			url:  fmt.Sprintf("%s/%08X", baseURL, c.ID),
			path: filepath.Join(targetDir, fmt.Sprintf("%08X.app", c.ID)),
		})
		if (c.Type & 0x02) != 0 {
			tasks = append(tasks, dlTask{
				url:  fmt.Sprintf("%s/%08X.h3", baseURL, c.ID),
				path: filepath.Join(targetDir, fmt.Sprintf("%08X.h3", c.ID)),
			})
		}
	}

	sem := make(chan struct{}, maxConcurrentDownloads)
	errChan := make(chan error, len(tasks))
	var wg sync.WaitGroup

	for _, t := range tasks {
		wg.Add(1)
		go func(task dlTask) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			select {
			case <-ctx.Done():
				errChan <- ctx.Err()
				return
			default:
			}

			if err := downloadFile(client, task.url, task.path, onProgress); err != nil {
				errChan <- err
			}
		}(t)
	}

	wg.Wait()
	close(errChan)

	for e := range errChan {
		if e != nil {
			return e
		}
	}

	if err := VerifyTitle(titleID, targetDir); err != nil {
		return fmt.Errorf("integrity check failed: %w", err)
	}

	if err := DecryptAndExtractTitle(targetDir, targetDir); err != nil {
		return fmt.Errorf("decryption/extraction failed: %w", err)
	}

	CleanRawNUSFiles(targetDir)

	if err := VerifyTitle(titleID, targetDir); err != nil {
		return fmt.Errorf("post-extraction verification failed: %w", err)
	}

	return nil
}
