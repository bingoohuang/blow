package main

import (
	"bytes"
	"fmt"
	"github.com/mattn/go-isatty"
	"github.com/mattn/go-runewidth"
	"io"
	"math"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	maxBarLen = 40
	barStart  = "|"
	barBody   = "■"
	barEnd    = "|"
)

var (
	barSpinner = []string{"|", "/", "-", "\\"}
	clearLine  = []byte("\r\033[K")
	isTerminal = isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())
)

type Printer struct {
	maxNum      int64
	maxDuration time.Duration
	curNum      int64
	curDuration time.Duration
	pbInc       int64
	pbNumStr    string
	pbDurStr    string
}

func NewPrinter(maxNum int64, maxDuration time.Duration) *Printer {
	return &Printer{maxNum: maxNum, maxDuration: maxDuration}
}

func (p *Printer) updateProgressValue(rs *SnapshotReport) {
	p.pbInc++
	if p.maxDuration > 0 {
		n := rs.Elapsed
		if n > p.maxDuration {
			n = p.maxDuration
		}
		p.curDuration = n
		barLen := int((p.curDuration*time.Duration(maxBarLen-2) + p.maxDuration/2) / p.maxDuration)
		p.pbDurStr = barStart + strings.Repeat(barBody, barLen) + strings.Repeat(" ", maxBarLen-2-barLen) + barEnd
	}
	if p.maxNum > 0 {
		p.curNum = rs.Count
		if p.maxNum > 0 {
			barLen := int((p.curNum*int64(maxBarLen-2) + p.maxNum/2) / p.maxNum)
			p.pbNumStr = barStart + strings.Repeat(barBody, barLen) + strings.Repeat(" ", maxBarLen-2-barLen) + barEnd
		} else {
			idx := p.pbInc % int64(len(barSpinner))
			p.pbNumStr = barSpinner[int(idx)]
		}
	}
}

func (p *Printer) PrintLoop(snapshot func() *SnapshotReport, interval time.Duration, useSeconds bool, doneChan <-chan struct{}, requests int64, logf *os.File) {
	if requests == 0 {
		select {}
	}

	var buf bytes.Buffer
	var backCursor string
	stdout := os.Stdout

	echo := func(isFinal bool) {
		r := snapshot()
		p.updateProgressValue(r)
		stdout.WriteString(backCursor)

		buf.Reset()
		p.formatTableReports(&buf, r, isFinal, useSeconds)

		n := printLines(buf.Bytes(), stdout)
		backCursor = fmt.Sprintf("\033[%dA", n)
		stdout.Sync()
	}

	if interval > 0 {
		ticker := time.NewTicker(interval)
	loop:
		for {
			select {
			case <-ticker.C:
				echo(false)
			case <-doneChan:
				ticker.Stop()
				break loop
			}
		}
	} else {
		<-doneChan
	}
	echo(true)

	if requests == 1 && logf != nil {
		if lastLog := getLastLog(logf); lastLog != "" {
			stdout.WriteString(lastLog)
		}
	}

	if logf != nil {
		logf.Write(buf.Bytes())
		_ = logf.Close()
	}
}

func getLastLog(f *os.File) string {
	found := false
	ch := make([]byte, 2)
	var cursor int64 = 0
	for {
		cursor -= 1
		_, err := f.Seek(cursor, io.SeekEnd)
		if err != nil {
			return ""
		}

		n, err := f.Read(ch)
		if err != nil {
			return ""
		}

		if n == 2 && ch[0] == '#' && ch[1] == ' ' { // stop if we find last log
			found = true
			break
		}
	}

	if !found {
		return ""
	}

	buffer := make([]byte, -cursor)
	n, _ := f.Read(buffer)
	return "\n# " + string(buffer[:n])
}

func printLines(result []byte, stdout *os.File) int {
	n := 0
	for ; ; n++ {
		i := bytes.IndexByte(result, '\n')
		if i < 0 {
			stdout.Write(clearLine)
			stdout.Write(result)
			break
		}
		stdout.Write(clearLine)
		stdout.Write(result[:i])
		stdout.Write([]byte("\n"))
		result = result[i+1:]
	}
	return n
}

//nolint
const (
	FgBlackColor int = iota + 30
	FgRedColor
	FgGreenColor
	FgYellowColor
	FgBlueColor
	FgMagentaColor
	FgCyanColor
	FgWhiteColor
)

func colorize(s string, seq int) string {
	if !isTerminal {
		return s
	}
	return fmt.Sprintf("\033[%dm%s\033[0m", seq, s)
}

func durationToString(d time.Duration, useSeconds bool) string {
	d = d.Truncate(time.Microsecond)
	if useSeconds {
		return formatFloat64(d.Seconds())
	}
	return d.String()
}

func alignBulk(bulk [][]string, aligns ...int) {
	maxLen := map[int]int{}
	for _, b := range bulk {
		for i, bb := range b {
			lbb := displayWidth(bb)
			if maxLen[i] < lbb {
				maxLen[i] = lbb
			}
		}
	}
	for _, b := range bulk {
		for i, ali := range aligns {
			if len(b) >= i+1 {
				if i == len(aligns)-1 && ali == AlignLeft {
					continue
				}
				b[i] = padString(b[i], " ", maxLen[i], ali)
			}
		}
	}
}

func writeBulkWith(w *bytes.Buffer, bulk [][]string, lineStart, sep, lineEnd string) {
	for _, b := range bulk {
		w.WriteString(lineStart)
		w.WriteString(b[0])
		for _, bb := range b[1:] {
			w.WriteString(sep)
			w.WriteString(bb)
		}
		w.WriteString(lineEnd)
	}
}

func writeBulk(w *bytes.Buffer, bulk [][]string) {
	writeBulkWith(w, bulk, "  ", "  ", "\n")
}

func formatFloat64(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func (p *Printer) formatTableReports(w *bytes.Buffer, r *SnapshotReport, isFinal bool, useSeconds bool) {
	w.WriteString("Summary:\n")
	writeBulk(w, p.buildSummary(r, isFinal))
	w.WriteString("\n")

	errorsBulks := p.buildErrors(r)
	if errorsBulks != nil {
		w.WriteString("Error:\n")
		writeBulk(w, errorsBulks)
		w.WriteString("\n")
	}

	writeBulkWith(w, p.buildStats(r, useSeconds), "", "  ", "\n")
	w.WriteString("\n")

	w.WriteString("Latency Percentile:\n")
	writeBulk(w, p.buildPercentile(r, useSeconds))
	w.WriteString("\n")

	w.WriteString("Latency Histogram:\n")
	writeBulk(w, p.buildHistogram(r, useSeconds, isFinal))
}

func (p *Printer) buildHistogram(r *SnapshotReport, useSeconds bool, isFinal bool) [][]string {
	hisBulk := make([][]string, 0, 8)
	maxCount := 0
	hisSum := 0
	for _, bin := range r.Histograms {
		if maxCount < bin.Count {
			maxCount = bin.Count
		}
		hisSum += bin.Count
	}
	for _, bin := range r.Histograms {
		row := []string{durationToString(bin.Mean, useSeconds), strconv.Itoa(bin.Count)}
		if isFinal {
			row = append(row, fmt.Sprintf("%.2f%%", math.Floor(float64(bin.Count)*1e4/float64(hisSum)+0.5)/100.0))
		} else {
			barLen := 0
			if maxCount > 0 {
				barLen = (bin.Count*maxBarLen + maxCount/2) / maxCount
			}
			row = append(row, strings.Repeat(barBody, barLen))
		}
		hisBulk = append(hisBulk, row)
	}
	if isFinal {
		alignBulk(hisBulk, AlignLeft, AlignRight, AlignRight)
	} else {
		alignBulk(hisBulk, AlignLeft, AlignRight, AlignLeft)
	}
	return hisBulk
}

func (p *Printer) buildPercentile(r *SnapshotReport, useSeconds bool) [][]string {
	percBulk := make([][]string, 2)
	percAligns := make([]int, 0, len(r.Percentiles))
	for _, percentile := range r.Percentiles {
		perc := formatFloat64(percentile.Percentile * 100)
		percBulk[0] = append(percBulk[0], "P"+perc)
		percBulk[1] = append(percBulk[1], durationToString(percentile.Latency, useSeconds))
		percAligns = append(percAligns, AlignCenter)
	}
	percAligns[0] = AlignLeft
	alignBulk(percBulk, percAligns...)
	return percBulk
}

func (p *Printer) buildStats(r *SnapshotReport, useSeconds bool) [][]string {
	dts := func(d time.Duration) string { return durationToString(d, useSeconds) }
	st := r.Stats
	statsBulk := [][]string{
		{"Statistics", "Min", "Mean", "StdDev", "Max"},
		{"  Latency", dts(st.Min), dts(st.Mean), dts(st.StdDev), dts(st.Max)}}
	rs := r.RpsStats
	if rs != nil {
		fft := func(v float64) string { return formatFloat64(math.Trunc(v*100) / 100.0) }
		statsBulk = append(statsBulk, []string{"  RPS", fft(rs.Min), fft(rs.Mean), fft(rs.StdDev), fft(rs.Max)})
	}
	alignBulk(statsBulk, AlignLeft, AlignCenter, AlignCenter, AlignCenter, AlignCenter)
	return statsBulk
}

func (p *Printer) buildErrors(r *SnapshotReport) [][]string {
	var errorsBulks [][]string
	for k, v := range r.Errors {
		vs := colorize(strconv.FormatInt(v, 10), FgRedColor)
		errorsBulks = append(errorsBulks, []string{vs, "\"" + k + "\""})
	}
	if errorsBulks != nil {
		sort.Slice(errorsBulks, func(i, j int) bool { return errorsBulks[i][1] < errorsBulks[j][1] })
	}
	alignBulk(errorsBulks, AlignLeft, AlignLeft)
	return errorsBulks
}

func (p *Printer) buildSummary(r *SnapshotReport, isFinal bool) [][]string {
	elapsedLine := []string{"Elapsed", r.Elapsed.Truncate(time.Millisecond).String()}
	if p.maxDuration > 0 && !isFinal {
		elapsedLine = append(elapsedLine, p.pbDurStr)
	}
	countLine := []string{"Count", strconv.FormatInt(r.Count, 10)}
	if p.maxNum > 0 && !isFinal {
		countLine = append(countLine, p.pbNumStr)
	}
	summaryBulk := [][]string{elapsedLine, countLine}

	codesBulks := make([][]string, 0, len(r.Codes))
	for k, v := range r.Codes {
		vs := strconv.FormatInt(v, 10)
		if k != "2xx" {
			vs = colorize(vs, FgMagentaColor)
		}
		codesBulks = append(codesBulks, []string{"  " + k, vs})
	}
	sort.Slice(codesBulks, func(i, j int) bool { return codesBulks[i][0] < codesBulks[j][0] })
	summaryBulk = append(summaryBulk, codesBulks...)
	summaryBulk = append(summaryBulk,
		[]string{"RPS", fmt.Sprintf("%.3f", r.RPS)},
		[]string{"Reads", fmt.Sprintf("%.3fMB/s", r.ReadThroughput)},
		[]string{"Writes", fmt.Sprintf("%.3fMB/s", r.WriteThroughput)},
		[]string{"Connections", fmt.Sprintf("%d", r.Connections)},
	)

	alignBulk(summaryBulk, AlignLeft, AlignRight)
	return summaryBulk
}

var ansi = regexp.MustCompile("\033\\[(?:[0-9]{1,3}(?:;[0-9]{1,3})*)?[m|K]")

func displayWidth(str string) int {
	return runewidth.StringWidth(ansi.ReplaceAllLiteralString(str, ""))
}

const (
	AlignLeft = iota
	AlignRight
	AlignCenter
)

func padString(s, pad string, width, align int) string {
	if gap := width - displayWidth(s); gap > 0 {
		switch align {
		case AlignLeft:
			return s + strings.Repeat(pad, gap)
		case AlignRight:
			return strings.Repeat(pad, gap) + s
		case AlignCenter:
			gapLeft := gap / 2
			gapRight := gap - gapLeft
			return strings.Repeat(pad, gapLeft) + s + strings.Repeat(pad, gapRight)
		}
	}
	return s
}
