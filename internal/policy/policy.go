package policy

import (
	"math"
	"sort"
	"time"

	"rto-tracker/internal/db"
)

// ComplianceStatus holds the result of evaluating a policy against badge records.
type ComplianceStatus struct {
	Policy          db.PolicyConfig `json:"policy"`
	WeekSummaries   []WeekDetail    `json:"week_summaries"`
	TotalBadged     int             `json:"total_badged"`
	RequiredTotal   int             `json:"required_total"`
	IsCompliant     bool            `json:"is_compliant"`
	AverageDays     float64         `json:"average_days"`     // average days/week in evaluated window
	DaysNeeded      int             `json:"days_needed"`      // additional days needed to be compliant
	WindowStart     string          `json:"window_start"`
	WindowEnd       string          `json:"window_end"`
	EvaluatedWeeks  int             `json:"evaluated_weeks"`  // number of weeks actually evaluated
	Message         string          `json:"message"`
}

// WeekDetail holds per-week information.
type WeekDetail struct {
	WeekStart string `json:"week_start"`
	WeekEnd   string `json:"week_end"`
	Count     int    `json:"count"`
	Required  int    `json:"required"`
	IsBest    bool   `json:"is_best"` // true if this week is in the "best" selection (BELT)
}

// Evaluate computes compliance status for the active policy.
func Evaluate(store *db.Store) (*ComplianceStatus, error) {
	pol, err := store.GetActivePolicy()
	if err != nil {
		return nil, err
	}
	if pol == nil {
		return &ComplianceStatus{Message: "No active policy configured."}, nil
	}

	now := time.Now()
	return EvaluateAt(store, pol, now)
}

// EvaluateAt computes compliance status for a policy at a specific point in time.
func EvaluateAt(store *db.Store, pol *db.PolicyConfig, now time.Time) (*ComplianceStatus, error) {
	// Find the Monday of the current week.
	currentMonday := startOfWeek(now)
	// The window goes back windowWeeks weeks from the START of the current week.
	windowStart := currentMonday.AddDate(0, 0, -7*(pol.WindowWeeks-1))
	windowEnd := currentMonday.AddDate(0, 0, 6) // Sunday of current week

	badges, err := store.GetBadgesInRange(
		windowStart.Format("2006-01-02"),
		windowEnd.Format("2006-01-02"),
	)
	if err != nil {
		return nil, err
	}

	// Build a set of badge dates.
	badgeSet := make(map[string]bool)
	for _, b := range badges {
		badgeSet[b.Date] = true
	}

	// Build weekly summaries.
	weeks := make([]WeekDetail, 0, pol.WindowWeeks)
	for i := 0; i < pol.WindowWeeks; i++ {
		monday := windowStart.AddDate(0, 0, 7*i)
		sunday := monday.AddDate(0, 0, 6)
		count := 0
		for d := monday; !d.After(sunday); d = d.AddDate(0, 0, 1) {
			if badgeSet[d.Format("2006-01-02")] {
				count++
			}
		}
		weeks = append(weeks, WeekDetail{
			WeekStart: monday.Format("2006-01-02"),
			WeekEnd:   sunday.Format("2006-01-02"),
			Count:     count,
			Required:  pol.DaysPerWeek,
		})
	}

	status := &ComplianceStatus{
		Policy:      *pol,
		WindowStart: windowStart.Format("2006-01-02"),
		WindowEnd:   windowEnd.Format("2006-01-02"),
	}

	switch pol.PolicyType {
	case "belt_average":
		evaluateBELTAverage(status, weeks, pol)
	case "belt_strict":
		evaluateBELTStrict(status, weeks, pol)
	case "straight":
		evaluateStraight(status, weeks, pol)
	default:
		evaluateBELTAverage(status, weeks, pol)
	}

	return status, nil
}

// evaluateBELTAverage: pick the best N weeks out of the window, compute average.
// Compliant if average >= daysPerWeek (i.e. total in best weeks >= daysPerWeek * bestWeeks).
func evaluateBELTAverage(status *ComplianceStatus, weeks []WeekDetail, pol *db.PolicyConfig) {
	bestWeeks := pol.BestWeeks
	if bestWeeks > len(weeks) {
		bestWeeks = len(weeks)
	}

	// Sort a copy by count descending to pick best weeks.
	sorted := make([]int, len(weeks))
	for i := range weeks {
		sorted[i] = i
	}
	sort.Slice(sorted, func(a, b int) bool {
		return weeks[sorted[a]].Count > weeks[sorted[b]].Count
	})

	bestSet := make(map[int]bool)
	totalBadged := 0
	for i := 0; i < bestWeeks; i++ {
		idx := sorted[i]
		bestSet[idx] = true
		totalBadged += weeks[idx].Count
	}

	// Mark best weeks.
	for i := range weeks {
		weeks[i].IsBest = bestSet[i]
	}

	requiredTotal := pol.DaysPerWeek * bestWeeks
	avg := float64(totalBadged) / float64(bestWeeks)
	daysNeeded := requiredTotal - totalBadged
	if daysNeeded < 0 {
		daysNeeded = 0
	}

	status.WeekSummaries = weeks
	status.TotalBadged = totalBadged
	status.RequiredTotal = requiredTotal
	status.IsCompliant = totalBadged >= requiredTotal
	status.AverageDays = math.Round(avg*100) / 100
	status.EvaluatedWeeks = bestWeeks
	if status.IsCompliant {
		status.Message = "You are compliant! Keep it up."
		status.DaysNeeded = 0
	} else {
		status.DaysNeeded = daysNeeded
		status.Message = "You need more badge-ins to meet compliance."
	}
}

// evaluateBELTStrict: pick the best N weeks, each must individually meet daysPerWeek.
func evaluateBELTStrict(status *ComplianceStatus, weeks []WeekDetail, pol *db.PolicyConfig) {
	bestWeeks := pol.BestWeeks
	if bestWeeks > len(weeks) {
		bestWeeks = len(weeks)
	}

	// Sort descending by count and pick best.
	sorted := make([]int, len(weeks))
	for i := range weeks {
		sorted[i] = i
	}
	sort.Slice(sorted, func(a, b int) bool {
		return weeks[sorted[a]].Count > weeks[sorted[b]].Count
	})

	bestSet := make(map[int]bool)
	totalBadged := 0
	compliantWeeks := 0
	for i := 0; i < bestWeeks; i++ {
		idx := sorted[i]
		bestSet[idx] = true
		totalBadged += weeks[idx].Count
		if weeks[idx].Count >= pol.DaysPerWeek {
			compliantWeeks++
		}
	}

	for i := range weeks {
		weeks[i].IsBest = bestSet[i]
	}

	requiredTotal := pol.DaysPerWeek * bestWeeks
	avg := float64(totalBadged) / float64(bestWeeks)

	status.WeekSummaries = weeks
	status.TotalBadged = totalBadged
	status.RequiredTotal = requiredTotal
	status.IsCompliant = compliantWeeks >= bestWeeks
	status.AverageDays = math.Round(avg*100) / 100
	status.EvaluatedWeeks = bestWeeks
	if status.IsCompliant {
		status.Message = "You are compliant! All best weeks meet the requirement."
		status.DaysNeeded = 0
	} else {
		deficit := 0
		for i := 0; i < bestWeeks; i++ {
			idx := sorted[i]
			if weeks[idx].Count < pol.DaysPerWeek {
				deficit += pol.DaysPerWeek - weeks[idx].Count
			}
		}
		status.DaysNeeded = deficit
		status.Message = "Some of your best weeks don't meet the per-week requirement."
	}
}

// evaluateStraight: every week in the window must meet daysPerWeek.
func evaluateStraight(status *ComplianceStatus, weeks []WeekDetail, pol *db.PolicyConfig) {
	totalBadged := 0
	failedWeeks := 0
	deficit := 0
	for i := range weeks {
		weeks[i].IsBest = true // all weeks count
		totalBadged += weeks[i].Count
		if weeks[i].Count < pol.DaysPerWeek {
			failedWeeks++
			deficit += pol.DaysPerWeek - weeks[i].Count
		}
	}

	requiredTotal := pol.DaysPerWeek * pol.WindowWeeks
	avg := float64(totalBadged) / float64(pol.WindowWeeks)

	status.WeekSummaries = weeks
	status.TotalBadged = totalBadged
	status.RequiredTotal = requiredTotal
	status.IsCompliant = failedWeeks == 0
	status.AverageDays = math.Round(avg*100) / 100
	status.EvaluatedWeeks = pol.WindowWeeks
	if status.IsCompliant {
		status.Message = "You are compliant! Every week meets the requirement."
		status.DaysNeeded = 0
	} else {
		status.DaysNeeded = deficit
		status.Message = "Some weeks are below the required days."
	}
}

// startOfWeek returns the Monday of the week containing t.
func startOfWeek(t time.Time) time.Time {
	t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	weekday := t.Weekday()
	if weekday == time.Sunday {
		weekday = 7
	}
	return t.AddDate(0, 0, -int(weekday-time.Monday))
}
