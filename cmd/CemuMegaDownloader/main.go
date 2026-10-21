package main

import (
	"bufio"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	cemumegadownloader "CemuMegaDownloader"
	"golang.org/x/sync/errgroup"
	"golang.org/x/term"
)

const (
	maxConcurrentTitles = 3
	barWidth            = 20
)

type KeyCode int

const (
	KeyNone KeyCode = iota
	KeyUp
	KeyDown
	KeySpace
	KeyEnter
	KeyToggleAll
	KeyQuit
)

type TitleItem struct {
	TitleID  string
	Name     string
	Path     string
	Selected bool
}

type SpeedEstimator struct {
	lastTime  time.Time
	lastBytes int64
	speedMBps float64
}

func newSpeedEstimator() *SpeedEstimator {
	return &SpeedEstimator{
		lastTime: time.Now(),
	}
}

func (se *SpeedEstimator) Update(currentBytes int64) float64 {
	now := time.Now()
	dt := now.Sub(se.lastTime).Seconds()
	if dt >= 0.25 {
		diff := currentBytes - se.lastBytes
		if diff < 0 {
			diff = 0
		}
		instant := (float64(diff) / (1024 * 1024)) / dt
		if se.speedMBps == 0 {
			se.speedMBps = instant
		} else {
			se.speedMBps = 0.3*instant + 0.7*se.speedMBps
		}
		se.lastTime = now
		se.lastBytes = currentBytes
	}
	return se.speedMBps
}

type ProgressReporterCLI struct {
	mu              sync.Mutex
	totalToDownload int64
	totalDownloaded int64
	startTime       time.Time
	lastPrintTime   time.Time
	totalTitles     int
	completedTitles int
	activeTitles    int
	exactSizeKnown  bool
	speedEstimator  *SpeedEstimator
}

func NewProgressReporterCLI() *ProgressReporterCLI {
	return &ProgressReporterCLI{
		speedEstimator: newSpeedEstimator(),
		startTime:      time.Now(),
	}
}

func (pr *ProgressReporterCLI) SetBatchTotals(totalTitles int, totalBytes int64) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	pr.totalToDownload = totalBytes
	pr.totalDownloaded = 0
	pr.startTime = time.Now()
	pr.lastPrintTime = time.Time{}
	pr.totalTitles = totalTitles
	pr.completedTitles = 0
	pr.activeTitles = 0
	pr.exactSizeKnown = totalBytes > 0
	pr.speedEstimator = newSpeedEstimator()
}

func (pr *ProgressReporterCLI) LogLine(format string, args ...any) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	fmt.Print("\r\033[2K")
	fmt.Printf(format+"\n", args...)
	pr.renderLocked(true)
}

func (pr *ProgressReporterCLI) ClearLine() {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	fmt.Print("\r\033[2K")
}

func formatBytes(b int64) string {
	if b < 0 {
		b = 0
	}
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d.Seconds())
	h := s / 3600
	m := (s % 3600) / 60
	sec := s % 60
	if h > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", h, m, sec)
	}
	return fmt.Sprintf("%02d:%02d", m, sec)
}

func makeProgressBar(percentage float64, width int) string {
	if percentage < 0 {
		percentage = 0
	}
	if percentage > 100 {
		percentage = 100
	}
	filled := int((percentage / 100.0) * float64(width))
	if filled > width {
		filled = width
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

func (pr *ProgressReporterCLI) AddBytes(n int64) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	pr.totalDownloaded += n
	if pr.totalDownloaded < 0 {
		pr.totalDownloaded = 0
	}
	now := time.Now()
	if now.Sub(pr.lastPrintTime) < 80*time.Millisecond {
		return
	}
	pr.lastPrintTime = now
	pr.renderLocked(false)
}

func (pr *ProgressReporterCLI) TitleStarted() {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	pr.activeTitles++
	pr.renderLocked(false)
}

func (pr *ProgressReporterCLI) TitleFinished(success bool) {
	pr.mu.Lock()
	defer pr.mu.Unlock()
	if pr.activeTitles > 0 {
		pr.activeTitles--
	}
	if success {
		pr.completedTitles++
	}
	pr.renderLocked(true)
}

func (pr *ProgressReporterCLI) renderLocked(force bool) {
	elapsed := time.Since(pr.startTime)
	speedMBps := pr.speedEstimator.Update(pr.totalDownloaded)
	if speedMBps <= 0 && elapsed.Seconds() > 0.1 {
		speedMBps = (float64(pr.totalDownloaded) / (1024 * 1024)) / elapsed.Seconds()
	}

	var percentage float64
	remainingStr := "--:--"

	if pr.totalToDownload > 0 {
		percentage = (float64(pr.totalDownloaded) / float64(pr.totalToDownload)) * 100.0
		if percentage > 100.0 {
			percentage = 100.0
		}
		if speedMBps > 0.01 && pr.totalToDownload > pr.totalDownloaded {
			remBytes := float64(pr.totalToDownload - pr.totalDownloaded)
			remSec := remBytes / (speedMBps * 1024 * 1024)
			remainingStr = formatDuration(time.Duration(remSec * float64(time.Second)))
		} else if pr.totalDownloaded >= pr.totalToDownload {
			remainingStr = "00:00"
		}
	} else if pr.totalTitles > 0 {
		percentage = (float64(pr.completedTitles) / float64(pr.totalTitles)) * 100.0
	}

	totalStr := formatBytes(pr.totalToDownload)
	if !pr.exactSizeKnown {
		totalStr = "?"
	}

	line := fmt.Sprintf("[%d/%d done | %d active] [%s] %5.1f%% | %s / %s | %.2f MB/s | %s < %s",
		pr.completedTitles,
		pr.totalTitles,
		pr.activeTitles,
		makeProgressBar(percentage, barWidth),
		percentage,
		formatBytes(pr.totalDownloaded),
		totalStr,
		speedMBps,
		formatDuration(elapsed),
		remainingStr,
	)

	termW, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || termW <= 10 {
		termW = 80
	}
	maxLen := max(20, termW-1)

	r := []rune(line)
	if len(r) > maxLen {
		line = string(r[:maxLen])
	}

	fmt.Printf("\r\033[2K%s", line)
}

func newHTTPClient() *http.Client {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           dialer.DialContext,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   100,
			MaxConnsPerHost:       100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 15 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}

func cleanPathInput(raw string) string {
	p := strings.TrimSpace(raw)
	p = strings.Trim(p, "\"'`")
	p = strings.TrimSpace(p)
	p = os.ExpandEnv(p)
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return filepath.Clean(p)
}

func checkDirectoryWritable(dir string) error {
	cleaned := cleanPathInput(dir)
	if err := os.MkdirAll(cleaned, 0755); err != nil {
		return err
	}
	testFile := filepath.Join(cleaned, fmt.Sprintf(".perm_test_%d", time.Now().UnixNano()))
	f, err := os.Create(testFile)
	if err != nil {
		return err
	}
	_ = f.Close()
	_ = os.Remove(testFile)
	return nil
}

func extractMLCPathFromSettingsXML(xmlPath string) string {
	data, err := os.ReadFile(xmlPath)
	if err != nil {
		return ""
	}

	type XMLSettings struct {
		XMLName xml.Name `xml:"content"`
		MLCPath string   `xml:"mlc_path"`
	}

	var s XMLSettings
	if err := xml.Unmarshal(data, &s); err == nil && strings.TrimSpace(s.MLCPath) != "" {
		return cleanPathInput(s.MLCPath)
	}

	type SimpleXML struct {
		MLCPath string `xml:"mlc_path"`
	}
	var s2 SimpleXML
	if err := xml.Unmarshal(data, &s2); err == nil && strings.TrimSpace(s2.MLCPath) != "" {
		return cleanPathInput(s2.MLCPath)
	}

	lines := strings.Split(string(data), "\n")
	for _, l := range lines {
		if strings.Contains(l, "<mlc_path>") && strings.Contains(l, "</mlc_path>") {
			start := strings.Index(l, "<mlc_path>") + len("<mlc_path>")
			end := strings.Index(l, "</mlc_path>")
			if start < end {
				return cleanPathInput(l[start:end])
			}
		}
	}

	return ""
}

func detectCemuMLCDirectory() string {
	var candidates []string
	homeDir, _ := os.UserHomeDir()

	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates, filepath.Join(exeDir, "portable"), exeDir)
	}
	candidates = append(candidates, "portable", ".", "..")

	switch runtime.GOOS {
	case "windows":
		if appdata := os.Getenv("APPDATA"); appdata != "" {
			candidates = append(candidates, filepath.Join(appdata, "Cemu"))
		}
		if localappdata := os.Getenv("LOCALAPPDATA"); localappdata != "" {
			candidates = append(candidates, filepath.Join(localappdata, "Cemu"))
		}
	case "darwin":
		if homeDir != "" {
			candidates = append(candidates, filepath.Join(homeDir, "Library", "Application Support", "Cemu"))
		}
	default:
		if xdgConfig := os.Getenv("XDG_CONFIG_HOME"); xdgConfig != "" {
			candidates = append(candidates, filepath.Join(xdgConfig, "Cemu"))
		}
		if xdgData := os.Getenv("XDG_DATA_HOME"); xdgData != "" {
			candidates = append(candidates, filepath.Join(xdgData, "Cemu"))
		}
		if homeDir != "" {
			candidates = append(candidates,
				filepath.Join(homeDir, ".local", "share", "Cemu"),
				filepath.Join(homeDir, ".config", "Cemu"),
				filepath.Join(homeDir, ".var", "app", "info.cemu.Cemu", "data", "cemu"),
				filepath.Join(homeDir, ".var", "app", "info.cemu.Cemu", "config", "cemu"),
			)
		}
	}

	for _, c := range candidates {
		xmlFile := filepath.Join(c, "settings.xml")
		if info, err := os.Stat(xmlFile); err == nil && !info.IsDir() {
			mlcCustom := extractMLCPathFromSettingsXML(xmlFile)
			if mlcCustom != "" {
				if infoMlc, err := os.Stat(mlcCustom); err == nil && infoMlc.IsDir() {
					return mlcCustom
				}
			}
			mlcDefault := filepath.Join(c, "mlc01")
			if infoMlc, err := os.Stat(mlcDefault); err == nil && infoMlc.IsDir() {
				return mlcDefault
			}
		}

		directMlc := filepath.Join(c, "mlc01")
		if infoMlc, err := os.Stat(directMlc); err == nil && infoMlc.IsDir() {
			return directMlc
		}
	}

	return ""
}

func resolveMLCDestPath(baseDir, titleID string) string {
	clean := cemumegadownloader.NormalizeTID(titleID)
	high := clean
	low := ""
	if len(clean) >= 16 {
		high = clean[:8]
		low = clean[8:]
	}

	subFolder := "sys"
	if high == "0005000b" || high == "00050000" || high == "00050002" || high == "0005000c" || high == "0005000e" {
		subFolder = "usr"
	}

	baseClean := cleanPathInput(baseDir)
	if strings.HasSuffix(strings.ToLower(baseClean), "mlc01") {
		if low != "" {
			return filepath.Join(baseClean, subFolder, "title", high, low)
		}
		return filepath.Join(baseClean, subFolder, "title", clean)
	}

	if low != "" {
		return filepath.Join(baseClean, "mlc01", subFolder, "title", high, low)
	}
	return filepath.Join(baseClean, "mlc01", subFolder, "title", clean)
}

func ensureTitlesJSONData(client *http.Client) ([]byte, error) {
	candidates := []string{"titles.json"}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "titles.json"))
	}

	for _, p := range candidates {
		if data, err := os.ReadFile(p); err == nil && len(data) > 64 {
			var cat cemumegadownloader.TitlesCatalog
			if json.Unmarshal(data, &cat) == nil && len(cat.EUR) > 0 && cat.EUR[0].Name != "" && cat.EUR[0].Name != cat.EUR[0].TitleID {
				return data, nil
			}
		}
	}

	fmt.Println("[Info] Generating titles.json...")
	return cemumegadownloader.GenerateTitlesJSON(client, "titles.json")
}

func LoadTitlesForRegion(region, baseDir string, client *http.Client) ([]TitleItem, error) {
	data, err := ensureTitlesJSONData(client)
	if err != nil {
		return nil, err
	}

	var cat cemumegadownloader.TitlesCatalog
	if err := json.Unmarshal(data, &cat); err != nil {
		return nil, fmt.Errorf("JSON parse error: %w", err)
	}

	var regionList []cemumegadownloader.TitleDefinition
	switch region {
	case "EUR":
		regionList = append(regionList, cat.EUR...)
	case "USA":
		regionList = append(regionList, cat.USA...)
	case "JPN":
		regionList = append(regionList, cat.JPN...)
	}
	regionList = append(regionList, cat.All...)

	seen := make(map[string]struct{})
	items := make([]TitleItem, 0, len(regionList))

	for _, entry := range regionList {
		norm := cemumegadownloader.NormalizeTID(entry.TitleID)
		if norm == "" || norm == "dummy" {
			continue
		}
		if _, ok := seen[norm]; !ok {
			seen[norm] = struct{}{}
			name := entry.Name
			if name == "" {
				name = norm
			}
			items = append(items, TitleItem{
				TitleID:  norm,
				Name:     name,
				Path:     resolveMLCDestPath(baseDir, norm),
				Selected: true,
			})
		}
	}

	return items, nil
}

func fitMiddle(s string, maxLen int) string {
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return string(r[:maxLen])
	}
	half := (maxLen - 3) / 2
	return string(r[:half]) + "..." + string(r[len(r)-(maxLen-3-half):])
}

func SelectTitlesInteractive(items []TitleItem, region string) ([]TitleItem, bool) {
	total := len(items)
	if total == 0 {
		return items, false
	}

	restoreRaw := enterRawTerminal()
	defer restoreRaw()

	flushStdin()
	cursor := 0
	scrollOffset := 0

	fmt.Print("\033[?1049h\033[?25l")
	defer fmt.Print("\033[?1049l\033[?25h")

	maxNameLen := 15
	for _, it := range items {
		if l := len([]rune(it.Name)); l > maxNameLen {
			maxNameLen = l
		}
	}

	lastW, lastH := 0, 0
	needRedraw := true

	for {
		termW, termH, err := term.GetSize(int(os.Stdout.Fd()))
		if err != nil || termW < 40 {
			termW = 80
		}
		if termH < 10 {
			termH = 24
		}

		if termW != lastW || termH != lastH {
			lastW = termW
			lastH = termH
			needRedraw = true
		}

		innerW := max(36, termW-2)
		pageSize := max(4, min(total, termH-8))

		cursor = max(0, min(cursor, total-1))

		if cursor < scrollOffset {
			scrollOffset = cursor
			needRedraw = true
		}
		if cursor >= scrollOffset+pageSize {
			scrollOffset = cursor - pageSize + 1
			needRedraw = true
		}
		scrollOffset = max(0, scrollOffset)
		if total > pageSize && scrollOffset > total-pageSize {
			scrollOffset = total - pageSize
		}

		if needRedraw {
			selectedCount := 0
			for _, it := range items {
				if it.Selected {
					selectedCount++
				}
			}

			fmt.Print("\033[H")

			printBordered := func(content string) {
				r := []rune(content)
				if len(r) > innerW {
					r = r[:innerW]
				}
				pad := max(0, innerW-len(r))
				fmt.Printf("\r\033[2K║%s%s║\n", string(r), strings.Repeat(" ", pad))
			}

			fmt.Printf("\r\033[2K╔%s╗\n", strings.Repeat("═", innerW))
			printBordered(fmt.Sprintf(" Title Selection: %-4s", region))
			printBordered(" [↑/↓] Navigate | [SPACE] Toggle | [A] Toggle All")
			printBordered(" [ENTER] Start Download | [Q/ESC] Cancel and Exit")

			showPathColumn := innerW >= 24+maxNameLen+4+15

			for i := 0; i < pageSize; i++ {
				idx := scrollOffset + i
				if idx >= total {
					break
				}

				prefix := "  "
				if idx == cursor {
					prefix = "> "
				}

				box := "[ ] "
				if items[idx].Selected {
					box = "[X] "
				}

				if showPathColumn {
					nameW := maxNameLen
					availPath := max(10, innerW-24-nameW-4)
					nameStr := fitMiddle(items[idx].Name, nameW)
					namePad := max(0, nameW-len([]rune(nameStr)))
					pathStr := fitMiddle(items[idx].Path, availPath)
					pathPad := max(0, availPath-len([]rune(pathStr)))
					rowContent := fmt.Sprintf("%s%s%s  %s%s -> %s%s",
						prefix, box, items[idx].TitleID, nameStr, strings.Repeat(" ", namePad), pathStr, strings.Repeat(" ", pathPad))
					printBordered(rowContent)
				} else {
					availName := innerW - 24
					nameStr := fitMiddle(items[idx].Name, availName)
					namePad := max(0, availName-len([]rune(nameStr)))
					rowContent := fmt.Sprintf("%s%s%s  %s%s", prefix, box, items[idx].TitleID, nameStr, strings.Repeat(" ", namePad))
					printBordered(rowContent)
				}
			}

			fmt.Printf("\r\033[2K╚%s╝\n", strings.Repeat("═", innerW))
			visibleEnd := min(total, scrollOffset+pageSize)
			fmt.Printf("\r\033[2K[Selected: %d / %d] (Scroll %d-%d of %d)\n",
				selectedCount, total, scrollOffset+1, visibleEnd, total)

			curPath := items[cursor].Path
			availStatus := max(10, termW-10)
			fmt.Printf("\r\033[2KTarget: %s\033[J", fitMiddle(curPath, availStatus))

			needRedraw = false
		}

		key := readKeyWithTimeout(40 * time.Millisecond)
		if key == KeyNone {
			continue
		}

		switch key {
		case KeyUp:
			if cursor > 0 {
				cursor--
				needRedraw = true
			}
		case KeyDown:
			if cursor < total-1 {
				cursor++
				needRedraw = true
			}
		case KeySpace:
			items[cursor].Selected = !items[cursor].Selected
			needRedraw = true
		case KeyToggleAll:
			selectedCount := 0
			for _, it := range items {
				if it.Selected {
					selectedCount++
				}
			}
			allChecked := selectedCount == total
			for i := range items {
				items[i].Selected = !allChecked
			}
			needRedraw = true
		case KeyEnter:
			var filtered []TitleItem
			for _, it := range items {
				if it.Selected {
					filtered = append(filtered, it)
				}
			}
			return filtered, true
		case KeyQuit:
			return nil, false
		}
	}
}

func readLineStdin(reader *bufio.Reader) string {
	line, _ := reader.ReadString('\n')
	return strings.TrimSpace(line)
}

func askYesNo(reader *bufio.Reader, prompt string, defaultYes bool) bool {
	for {
		fmt.Print(prompt)
		ans := strings.ToLower(readLineStdin(reader))
		if ans == "" {
			return defaultYes
		}
		if ans == "y" || ans == "yes" {
			return true
		}
		if ans == "n" || ans == "no" {
			return false
		}
		fmt.Println("Invalid input. Please enter 'y' or 'n'.")
	}
}

func main() {
	cleanupVT := enableVirtualTerminal()
	defer cleanupVT()

	if len(os.Args) > 1 && os.Args[1] == "--generate-titles" {
		client := newHTTPClient()
		if _, err := cemumegadownloader.GenerateTitlesJSON(client, "titles.json"); err != nil {
			fmt.Fprintf(os.Stderr, "[Error] %v\n", err)
			os.Exit(1)
		}
		fmt.Println("[Info] titles.json generated successfully.")
		return
	}

	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Println("Menu:")
		fmt.Println("1. Download EUR titles")
		fmt.Println("2. Download USA titles")
		fmt.Println("3. Download JPN titles")
		fmt.Println("4. Exit")
		fmt.Print("Select an option: ")

		choice := readLineStdin(reader)
		switch choice {
		case "1":
			startRegionWorkflow("EUR", reader)
			return
		case "2":
			startRegionWorkflow("USA", reader)
			return
		case "3":
			startRegionWorkflow("JPN", reader)
			return
		case "4":
			return
		default:
			fmt.Println("Invalid choice. Try again.")
		}
	}
}

func startRegionWorkflow(region string, reader *bufio.Reader) {
	client := newHTTPClient()

	var targetBaseDir string
	cleanInstall := false

	detectedMlc := detectCemuMLCDirectory()
	if detectedMlc != "" {
		absMlc, _ := filepath.Abs(detectedMlc)
		fmt.Printf("\n[Cemu Detected] mlc01 directory found at:\n  %s\n", absMlc)
		if err := checkDirectoryWritable(absMlc); err == nil {
			if askYesNo(reader, "Do you want to install titles directly into this Cemu folder? [Y/n]: ", true) {
				targetBaseDir = absMlc
				fmt.Printf("[Info] Destination set to: %s\n", targetBaseDir)
			}
		} else {
			fmt.Printf("[Warning] Missing write permissions for %s (%v).\n", absMlc, err)
		}
	}

	if targetBaseDir == "" {
		if askYesNo(reader, "\nDo you want to specify a custom mlc01 path? [y/N]: ", false) {
			for {
				fmt.Print("Enter custom mlc01 path (or press Enter to cancel): ")
				inputPath := readLineStdin(reader)
				if inputPath == "" {
					break
				}
				cleaned := cleanPathInput(inputPath)
				if err := checkDirectoryWritable(cleaned); err == nil {
					absCustom, _ := filepath.Abs(cleaned)
					targetBaseDir = absCustom
					fmt.Printf("[Info] Custom destination set to: %s\n", targetBaseDir)
					break
				} else {
					fmt.Printf("[Warning] Path '%s' is not writable or invalid: %v\n", cleaned, err)
					fmt.Println("Please try again.")
				}
			}
		}
	}

	if targetBaseDir == "" {
		targetBaseDir = "."
		fmt.Println("[Info] Using default local directory (./mlc01).")
		if err := checkDirectoryWritable("mlc01"); err != nil {
			fmt.Printf("[Critical Error] Unable to write to local mlc01 directory: %v\n", err)
			return
		}
	}

	mlcRoot := cleanPathInput(targetBaseDir)
	if !strings.HasSuffix(strings.ToLower(mlcRoot), "mlc01") {
		mlcRoot = filepath.Join(mlcRoot, "mlc01")
	}

	usrDir := filepath.Join(mlcRoot, "usr")
	if entries, err := os.ReadDir(usrDir); err == nil && len(entries) > 0 {
		fmt.Printf("\n[Notice] Existing 'usr' directory detected at:\n  %s\n", usrDir)
		fmt.Println("[Notice] It is strongly recommended to back up your 'usr' directory in case you have important game saves.")
	}

	sysTitleDir := filepath.Join(mlcRoot, "sys", "title")
	if entries, err := os.ReadDir(sysTitleDir); err == nil && len(entries) > 0 {
		fmt.Println("\nExisting system titles were detected in this mlc01 directory.")
		if askYesNo(reader, "Do you want to perform a clean installation (purge previous 'sys' title data)? [y/N]: ", false) {
			cleanInstall = true
			_ = os.RemoveAll(sysTitleDir)
			_ = os.MkdirAll(sysTitleDir, 0755)
			fmt.Println("[Info] Clean installation enabled: 'sys/title' has been purged. 'usr' was left untouched.")
		}
	}

	fmt.Printf("\n[Info] Loading MLC title list for region %s...\n", region)
	availableItems, err := LoadTitlesForRegion(region, targetBaseDir, client)
	if err != nil {
		fmt.Printf("[Error] Failed to load titles: %v\n", err)
		return
	}

	selectedItems, ok := SelectTitlesInteractive(availableItems, region)
	if !ok || len(selectedItems) == 0 {
		fmt.Println("[Info] No titles selected or operation cancelled.")
		return
	}

	downloadBatch(selectedItems, region, client, cleanInstall)
}

func downloadBatch(chosenTitles []TitleItem, region string, client *http.Client, cleanInstall bool) {
	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	totalTitles := len(chosenTitles)

	var (
		failedMu     sync.Mutex
		failedTitles []string
		verifiedCnt  int32
	)

	fmt.Printf("\n[Info] Scanning metadata for %d titles to calculate exact batch size...\n", totalTitles)

	var (
		totalBatchBytes int64
		sizeMu          sync.Mutex
		scanGroup       errgroup.Group
	)
	scanGroup.SetLimit(10)

	for _, item := range chosenTitles {
		tid := item.TitleID
		scanGroup.Go(func() error {
			if ctx.Err() != nil {
				return nil
			}
			sz, err := cemumegadownloader.GetTitleSize(tid, client)
			if err == nil && sz > 0 {
				sizeMu.Lock()
				totalBatchBytes += sz
				sizeMu.Unlock()
			}
			return nil
		})
	}
	_ = scanGroup.Wait()

	progressReporter := NewProgressReporterCLI()
	progressReporter.SetBatchTotals(totalTitles, totalBatchBytes)

	if totalBatchBytes > 0 {
		fmt.Printf("[Info] Total size: %s (%d bytes)\n", formatBytes(totalBatchBytes), totalBatchBytes)
	}

	fmt.Printf("\n[Info] Starting concurrent download of %d titles for region %s (%d titles in parallel)...\n\n",
		totalTitles, region, maxConcurrentTitles)

	var g errgroup.Group
	g.SetLimit(maxConcurrentTitles)

	for _, item := range chosenTitles {
		g.Go(func() error {
			if ctx.Err() != nil {
				return nil
			}
			progressReporter.TitleStarted()
			progressReporter.LogLine("[Info] Downloading %s (%s)", item.TitleID, item.Name)

			err := cemumegadownloader.DownloadAndExtractTitle(
				ctx,
				item.TitleID,
				item.Path,
				client,
				cleanInstall,
				progressReporter.AddBytes,
			)
			if err != nil {
				progressReporter.TitleFinished(false)
				progressReporter.LogLine("[Error] Title %s failed: %v", item.TitleID, err)
				failedMu.Lock()
				failedTitles = append(failedTitles, item.TitleID)
				failedMu.Unlock()
				return nil
			}

			progressReporter.TitleFinished(true)
			doneNow := atomic.AddInt32(&verifiedCnt, 1)
			progressReporter.LogLine("[Info] [%d/%d] %s (%s) verified and extracted OK",
				doneNow, totalTitles, item.TitleID, item.Name)
			return nil
		})
	}

	_ = g.Wait()
	progressReporter.ClearLine()

	fmt.Printf("\n[Info] Running final completeness and integrity check on all %d titles...\n", totalTitles)
	var finalMissingOrCorrupt []string
	for idx, item := range chosenTitles {
		if err := cemumegadownloader.VerifyTitle(item.TitleID, item.Path); err != nil {
			fmt.Printf("[Error] [%d/%d] Final check FAILED for %s: %v\n", idx+1, totalTitles, item.TitleID, err)
			finalMissingOrCorrupt = append(finalMissingOrCorrupt, item.TitleID)
		}
	}

	fmt.Printf("\n[Info] Final Summary for %s: %d/%d titles present (extracted to mlc01 and verified)\n",
		region, totalTitles-len(finalMissingOrCorrupt), totalTitles)

	if len(finalMissingOrCorrupt) == 0 && len(failedTitles) == 0 {
		fmt.Println("[Info] All done! Every single title is complete, verified, and ready for Cemu.")
		fmt.Println("Press Enter to exit...")
		_, _ = bufio.NewReader(os.Stdin).ReadBytes('\n')
		os.Exit(0)
	} else {
		fmt.Printf("[Error] Titles failing verification: %v\n", finalMissingOrCorrupt)
		fmt.Println("[Error] Some titles were not able to be downloaded or verified.")
		fmt.Println("Press Enter to exit...")
		_, _ = bufio.NewReader(os.Stdin).ReadBytes('\n')
		os.Exit(1)
	}
}
