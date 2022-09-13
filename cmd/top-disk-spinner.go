// Copyright (c) 2015-2022 MinIO, Inc.
//
// This file is part of MinIO Object Storage stack
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/minio/madmin-go"
	"github.com/olekukonko/tablewriter"
)

type topDiskUI struct {
	spinner  spinner.Model
	quitting bool

	sortBy        sortIOStat
	count         int
	pool, maxPool int

	disksInfo map[string]madmin.Disk

	prevTopMap map[string]madmin.DiskIOStats
	currTopMap map[string]madmin.DiskIOStats
}

type topDiskResult struct {
	final    bool
	diskName string
	stats    madmin.DiskIOStats
}

func initTopDiskUI(disks []madmin.Disk, count int) *topDiskUI {
	maxPool := 0
	disksInfo := make(map[string]madmin.Disk)
	for i := range disks {
		disksInfo[disks[i].Endpoint] = disks[i]
		if disks[i].PoolIndex > maxPool {
			maxPool = disks[i].PoolIndex
		}
	}

	s := spinner.New()
	s.Spinner = spinner.Points
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	return &topDiskUI{
		count:      count,
		sortBy:     sortByName,
		pool:       0,
		maxPool:    maxPool,
		disksInfo:  disksInfo,
		spinner:    s,
		prevTopMap: make(map[string]madmin.DiskIOStats),
		currTopMap: make(map[string]madmin.DiskIOStats),
	}
}

func (m *topDiskUI) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m *topDiskUI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q", "esc":
			m.quitting = true
			return m, tea.Quit
		case "right":
			m.pool++
			if m.pool >= m.maxPool {
				m.pool = m.maxPool
			}
		case "left":
			m.pool--
			if m.pool < 0 {
				m.pool = 0
			}
		case "u":
			m.sortBy = sortByUsed
		case "t":
			m.sortBy = sortByTps
		case "r":
			m.sortBy = sortByRead
		case "w":
			m.sortBy = sortByWrite
		case "A":
			m.sortBy = sortByAwait
		case "U":
			m.sortBy = sortByUtil
		}

		return m, nil
	case topDiskResult:
		m.prevTopMap[msg.diskName] = m.currTopMap[msg.diskName]
		m.currTopMap[msg.diskName] = msg.stats
		if msg.final {
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	default:
		return m, nil
	}
}

type diskIOStat struct {
	endpoint   string
	util       float64
	await      float64
	readMBs    float64
	writeMBs   float64
	discardMBs float64
	tps        uint64
	used       uint64
}

func generateDiskStat(disk madmin.Disk, curr, prev madmin.DiskIOStats, interval uint64) (d diskIOStat) {
	d.endpoint = disk.Endpoint
	d.used = 100 * disk.UsedSpace / disk.TotalSpace
	d.util = 100 * float64(curr.TotalTicks-prev.TotalTicks) / float64(interval)
	currTotalIOs := curr.ReadIOs + curr.WriteIOs + curr.DiscardIOs
	prevTotalIOs := prev.ReadIOs + prev.WriteIOs + prev.DiscardIOs
	totalTicksDiff := curr.ReadTicks - prev.ReadTicks + curr.WriteTicks - prev.WriteTicks + curr.DiscardTicks - prev.DiscardTicks
	if currTotalIOs > prevTotalIOs {
		d.tps = currTotalIOs - prevTotalIOs
		d.await = float64(totalTicksDiff) / float64(currTotalIOs-prevTotalIOs)
	}
	intervalInSec := float64(interval / 1000)
	d.readMBs = float64(curr.ReadSectors-prev.ReadSectors) / (2048 * intervalInSec)
	d.writeMBs = float64(curr.WriteSectors-prev.WriteSectors) / (2048 * intervalInSec)
	d.discardMBs = float64(curr.DiscardSectors-prev.DiscardSectors) / (2048 * intervalInSec)
	return d
}

type sortIOStat int

const (
	sortByName sortIOStat = iota
	sortByUsed
	sortByAwait
	sortByUtil
	sortByRead
	sortByWrite
	sortByDiscard
	sortByTps
)

func (s sortIOStat) String() string {
	switch s {
	case sortByName:
		return "name"
	case sortByUsed:
		return "used"
	case sortByAwait:
		return "await"
	case sortByUtil:
		return "util"
	case sortByRead:
		return "read"
	case sortByWrite:
		return "write"
	case sortByDiscard:
		return "discard"
	case sortByTps:
		return "tps"
	}
	return "unknown"
}

func (m *topDiskUI) View() string {
	var s strings.Builder
	s.WriteString("\n")

	// Set table header
	table := tablewriter.NewWriter(&s)
	table.SetAutoWrapText(false)
	table.SetAutoFormatHeaders(true)
	table.SetHeaderAlignment(tablewriter.ALIGN_CENTER)
	table.SetAlignment(tablewriter.ALIGN_CENTER)
	table.SetCenterSeparator("")
	table.SetColumnSeparator("")
	table.SetRowSeparator("")
	table.SetHeaderLine(false)
	table.SetBorder(false)
	table.SetTablePadding("\t") // pad with tabs
	table.SetNoWhiteSpace(true)

	table.SetHeader([]string{"Disk", "used", "tps", "read", "write", "discard", "await", "util"})

	var data []diskIOStat

	for disk := range m.currTopMap {
		currDisk, ok := m.disksInfo[disk]
		if !ok || currDisk.PoolIndex != m.pool {
			continue
		}
		data = append(data, generateDiskStat(m.disksInfo[disk], m.currTopMap[disk], m.prevTopMap[disk], 1000))
	}

	sort.Slice(data, func(i, j int) bool {
		switch m.sortBy {
		case sortByName:
			return data[i].endpoint < data[j].endpoint
		case sortByUsed:
			return data[i].used > data[j].used
		case sortByAwait:
			return data[i].await > data[j].await
		case sortByUtil:
			return data[i].util > data[j].util
		case sortByRead:
			return data[i].readMBs < data[j].readMBs
		case sortByWrite:
			return data[i].writeMBs < data[j].writeMBs
		case sortByDiscard:
			return data[i].discardMBs > data[j].discardMBs
		case sortByTps:
			return data[i].tps < data[j].tps
		}
		return false
	})

	if len(data) > m.count {
		data = data[:m.count]
	}

	dataRender := make([][]string, 0, len(data))
	for _, d := range data {
		endpoint := d.endpoint
		diskInfo := m.disksInfo[endpoint]
		if diskInfo.Healing {
			endpoint += "!"
		}
		if diskInfo.Scanning {
			endpoint += "*"
		}

		dataRender = append(dataRender, []string{
			endpoint,
			whiteStyle.Render(fmt.Sprintf("%d%%", d.used)),
			whiteStyle.Render(fmt.Sprintf("%v", d.tps)),
			whiteStyle.Render(fmt.Sprintf("%.2f MiB/s", d.readMBs)),
			whiteStyle.Render(fmt.Sprintf("%.2f MiB/s", d.writeMBs)),
			whiteStyle.Render(fmt.Sprintf("%.2f MiB/s", d.discardMBs)),
			whiteStyle.Render(fmt.Sprintf("%.1f ms", d.await)),
			whiteStyle.Render(fmt.Sprintf("%.1f%%", d.util)),
		})
	}

	table.AppendBulk(dataRender)
	table.Render()

	if !m.quitting {
		s.WriteString(fmt.Sprintf("\n%s \u25C0 Pool %d \u25B6 | Sort By: %s (u,t,r,w,d,A,U)", m.spinner.View(), m.pool+1, m.sortBy))
	}
	return s.String()
}