package api

import (
	"encoding/csv"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"opskest/internal/model"
	"opskest/internal/store"
)

// Monthly SLA report (GET /api/reports/sla). Availability is time-based:
// 1 − (outage minutes clipped to the month) / (minutes the monitor
// existed in the month so far) — outages never open during maintenance,
// so planned work doesn't count against it. Check success % (from the
// daily rollups) and p50/p95/p99 (daily histograms) are shown alongside.
// The error budget is (1 − target) × the whole month's minutes.

type slaRow struct {
	ID              int64    `json:"id"`
	Name            string   `json:"name"`
	Group           string   `json:"group"`
	AvailabilityPct *float64 `json:"availabilityPct"` // nil: monitor didn't exist this month
	CheckUptimePct  *float64 `json:"checkUptimePct"`
	Checks          int      `json:"checks"`
	DowntimeMin     float64  `json:"downtimeMin"`
	Outages         int      `json:"outages"`
	LongestMin      float64  `json:"longestMin"`
	MttrMin         *float64 `json:"mttrMin"`
	P50             *int     `json:"p50"`
	P95             *int     `json:"p95"`
	P99             *int     `json:"p99"`
	Met             *bool    `json:"met"`
	BudgetMin       float64  `json:"budgetMin"`
	BudgetUsedPct   float64  `json:"budgetUsedPct"`
}

type slaSummary struct {
	Monitors        int      `json:"monitors"`
	Met             int      `json:"met"`
	Breached        int      `json:"breached"`
	AvailabilityPct *float64 `json:"availabilityPct"` // mean over monitors with data
	DowntimeMin     float64  `json:"downtimeMin"`
	Outages         int      `json:"outages"`
	WorstID         *int64   `json:"worstId"`
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// handleSLAReport serves GET /api/reports/sla?month=YYYY-MM&scope=monitor|group|page|all&id=&target=99.9&format=json|csv&limit=&offset=.
func (s *server) handleSLAReport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	loc := s.d.Store.Loc
	now := s.d.Now()

	monthStr := q.Get("month")
	if monthStr == "" {
		monthStr = now.In(loc).Format("2006-01")
	}
	mt, err := time.ParseInLocation("2006-01", monthStr, loc)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "month must be YYYY-MM", nil)
		return
	}
	monthStart, monthEnd := mt, mt.AddDate(0, 1, 0)
	if monthStart.After(now) || now.Sub(monthStart) > 730*24*time.Hour {
		writeErr(w, http.StatusBadRequest, "month must be within the last 24 months and not in the future", nil)
		return
	}
	target := 99.9
	if t := q.Get("target"); t != "" {
		v, err := strconv.ParseFloat(t, 64)
		if err != nil || v <= 0 || v >= 100 {
			writeErr(w, http.StatusBadRequest, "target must be a percentage between 0 and 100", nil)
			return
		}
		target = v
	}

	all := s.d.Engine.Monitors()
	byID := make(map[int64]*model.Monitor, len(all))
	for _, m := range all {
		byID[m.ID] = m
	}
	scope := q.Get("scope")
	if scope == "" {
		scope = "all"
	}
	id := queryInt64(r, "id", 0)
	var ids []int64
	scopeName := "All monitors"
	switch scope {
	case "all":
		for _, m := range all {
			if !m.Settings.Vendor {
				ids = append(ids, m.ID)
			}
		}
	case "monitor":
		m, ok := byID[id]
		if !ok {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		if m.Settings.Vendor { // 3rd-party feeds have no uptime or SLA
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		ids, scopeName = []int64{id}, m.Name
	case "group":
		found := false
		for _, g := range s.d.Engine.Groups() {
			if g.ID == id {
				found, scopeName = true, g.Name
			}
		}
		if !found {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		ids = s.d.Engine.MonitorIDsInGroup(id)
	case "page":
		page, err := s.d.Store.GetPage(r.Context(), id)
		if err != nil {
			writeErr(w, http.StatusNotFound, "not found", nil)
			return
		}
		scopeName = page.Title
		for mid := range s.pageMonitorIDs(page) {
			ids = append(ids, mid)
		}
	default:
		writeErr(w, http.StatusBadRequest, "scope must be monitor, group, page or all", nil)
		return
	}

	rows, err := s.buildSLARows(r, ids, byID, monthStart, monthEnd, now, target)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "report failed", nil)
		return
	}
	sum := summarizeSLA(rows)

	if q.Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="sla-%s-%s.csv"`, monthStr, scope))
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"monitor_id", "monitor", "group", "availability_pct", "target_pct", "met", "downtime_min", "outages", "longest_min", "mttr_min", "check_uptime_pct", "checks", "p50_ms", "p95_ms", "p99_ms", "budget_min", "budget_used_pct"})
		for _, row := range rows {
			_ = cw.Write([]string{
				strconv.FormatInt(row.ID, 10), csvSafe(row.Name), csvSafe(row.Group), fmtPtrF(row.AvailabilityPct, 3), strconv.FormatFloat(target, 'f', -1, 64),
				fmtPtrB(row.Met), strconv.FormatFloat(row.DowntimeMin, 'f', 1, 64), strconv.Itoa(row.Outages),
				strconv.FormatFloat(row.LongestMin, 'f', 1, 64), fmtPtrF(row.MttrMin, 1), fmtPtrF(row.CheckUptimePct, 3),
				strconv.Itoa(row.Checks), fmtPtrI(row.P50), fmtPtrI(row.P95), fmtPtrI(row.P99),
				strconv.FormatFloat(row.BudgetMin, 'f', 1, 64), strconv.FormatFloat(row.BudgetUsedPct, 'f', 1, 64),
			})
		}
		cw.Flush()
		return
	}

	// JSON: everything summarised, rows paged (worst availability first).
	limit := clampLimit(queryInt(r, "limit", 50))
	offset := queryInt(r, "offset", 0)
	if offset < 0 || offset > len(rows) {
		offset = len(rows)
	}
	end := offset + limit
	if end > len(rows) {
		end = len(rows)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"month": monthStr, "scope": scope, "scopeName": scopeName, "targetPct": target,
		"monthToDate": now.Before(monthEnd), "from": monthStart.UnixMilli(), "to": monthEnd.UnixMilli(),
		"summary": sum, "total": len(rows), "limit": limit, "offset": offset, "rows": rows[offset:end],
	})
}

func (s *server) buildSLARows(r *http.Request, ids []int64, byID map[int64]*model.Monitor, monthStart, monthEnd, now time.Time, target float64) ([]slaRow, error) {
	from, to := monthStart.UnixMilli(), monthEnd.UnixMilli()
	end := monthEnd
	if now.Before(end) {
		end = now
	}
	rollups, err := s.d.Store.RollupHists(r.Context(), "rollup_daily", from, to-1)
	if err != nil {
		return nil, err
	}
	outs, err := s.d.Store.OutagesOverlapping(r.Context(), from, end.UnixMilli())
	if err != nil {
		return nil, err
	}
	byMon := map[int64][]store.Outage{}
	for _, o := range outs {
		byMon[o.MonitorID] = append(byMon[o.MonitorID], o)
	}
	groupName := map[int64]string{}
	for _, g := range s.d.Engine.Groups() {
		groupName[g.ID] = g.Name
	}
	monthMin := monthEnd.Sub(monthStart).Minutes()
	budget := (1 - target/100) * monthMin

	rows := make([]slaRow, 0, len(ids))
	for _, id := range ids {
		m, ok := byID[id]
		if !ok || m.Settings.Vendor {
			continue
		}
		row := slaRow{ID: id, Name: m.Name, BudgetMin: round2(budget)}
		if m.GroupID != nil {
			row.Group = groupName[*m.GroupID]
		}
		// Minutes the monitor existed in the month so far.
		start := monthStart
		if m.CreatedAt.After(start) {
			start = m.CreatedAt
		}
		exist := end.Sub(start).Minutes()

		var repairs []float64
		for _, o := range byMon[id] {
			oStart, oEnd := time.UnixMilli(o.StartedAt), end
			if o.EndedAt != nil {
				oEnd = time.UnixMilli(*o.EndedAt)
			}
			if oStart.Before(monthStart) {
				oStart = monthStart
			}
			if oEnd.After(end) {
				oEnd = end
			}
			if !oEnd.After(oStart) {
				continue
			}
			mins := oEnd.Sub(oStart).Minutes()
			row.DowntimeMin += mins
			row.Outages++
			if mins > row.LongestMin {
				row.LongestMin = mins
			}
			if o.EndedAt != nil {
				repairs = append(repairs, time.UnixMilli(*o.EndedAt).Sub(time.UnixMilli(o.StartedAt)).Minutes())
			}
		}
		row.DowntimeMin, row.LongestMin = round2(row.DowntimeMin), round2(row.LongestMin)
		if len(repairs) > 0 {
			var t float64
			for _, v := range repairs {
				t += v
			}
			v := round2(t / float64(len(repairs)))
			row.MttrMin = &v
		}
		if exist > 0 {
			avail := 100 * (1 - row.DowntimeMin/exist)
			if avail < 0 {
				avail = 0
			}
			avail = math.Round(avail*1000) / 1000
			row.AvailabilityPct = &avail
			met := avail >= target
			row.Met = &met
		}
		if budget > 0 {
			row.BudgetUsedPct = round2(100 * row.DowntimeMin / budget)
		}
		if ru, ok := rollups[id]; ok {
			row.Checks = ru.Up + ru.Degraded + ru.Pending + ru.Down
			if v, ok := ru.Uptime(); ok {
				v = math.Round(v*1000) / 1000
				row.CheckUptimePct = &v
			}
			st := statsFromHist(ru.Histogram(), ru.LatSum, ru.LatCount, ru.LatMin, ru.LatMax)
			row.P50, row.P95, row.P99 = st.P50, st.P95, st.P99
		}
		rows = append(rows, row)
	}
	// Worst first; monitors with no data this month last.
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i].AvailabilityPct, rows[j].AvailabilityPct
		switch {
		case a == nil:
			return false
		case b == nil:
			return true
		case *a != *b:
			return *a < *b
		}
		return rows[i].Name < rows[j].Name
	})
	return rows, nil
}

func summarizeSLA(rows []slaRow) slaSummary {
	sum := slaSummary{Monitors: len(rows)}
	var total float64
	n := 0
	for i := range rows {
		row := rows[i]
		sum.DowntimeMin += row.DowntimeMin
		sum.Outages += row.Outages
		if row.Met != nil {
			if *row.Met {
				sum.Met++
			} else {
				sum.Breached++
			}
		}
		if row.AvailabilityPct != nil {
			total += *row.AvailabilityPct
			n++
			if sum.WorstID == nil {
				id := row.ID // rows are sorted worst first
				sum.WorstID = &id
			}
		}
	}
	sum.DowntimeMin = round2(sum.DowntimeMin)
	if n > 0 {
		v := math.Round(total/float64(n)*1000) / 1000
		sum.AvailabilityPct = &v
	}
	return sum
}

// csvSafe defuses spreadsheet formula injection: a cell starting with
// = + - @ (or a tab/CR) is evaluated by Excel/Sheets, so it gets a
// leading apostrophe and is shown as text.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

func fmtPtrF(p *float64, prec int) string {
	if p == nil {
		return ""
	}
	return strconv.FormatFloat(*p, 'f', prec, 64)
}

func fmtPtrI(p *int) string {
	if p == nil {
		return ""
	}
	return strconv.Itoa(*p)
}

func fmtPtrB(p *bool) string {
	if p == nil {
		return ""
	}
	if *p {
		return "yes"
	}
	return "no"
}
