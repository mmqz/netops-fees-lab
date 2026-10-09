// Lab report of the simulation workbench (PROJECT_BRIEF_P2 §7:
// GET /api/lab/report?workspaceId=&format=html|json). The JSON shape is the
// pinned contract of the route; the standalone HTML rendering derives from
// the same struct plus the workspace device inventory (a table the JSON
// contract deliberately does not carry), in the Phase 1 report visual style.

package report

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"strconv"
	"strings"
	"time"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/repo"
)

// labTemplateHTML is the standalone HTML template of the lab report;
// html/template auto-escapes every dynamic value it interpolates.
//
//go:embed labtemplate.html
var labTemplateHTML string

// labTmpl parses the lab template once at package initialization; a
// malformed template is a programming error and panics at startup
// (template.Must).
var labTmpl = template.Must(template.New("labreport").Parse(labTemplateHTML))

// LabDisclaimer is the pinned synthetic-data disclaimer of every lab report
// (identical to the Phase 1 quality report footer).
const LabDisclaimer = "本报告基于合成测试数据生成，仅用于工程方法验证，不代表真实网络运行情况。"

// labReportChangeSetLimit caps the changeset history section of the report.
const labReportChangeSetLimit = 10

// WorkspaceHasher abstracts the simulation engine's canonical workspace
// state hash for the lab report generator. *sim.Service satisfies it; the
// interface keeps the report package free of a sim dependency.
type WorkspaceHasher interface {
	WorkspaceHash(ctx context.Context, workspaceID string) (string, error)
}

// LabGenerator builds lab workspace reports from the repository state.
type LabGenerator struct {
	db   *repo.DB
	hash WorkspaceHasher

	// Now is the clock hook stamping generatedAt; tests may replace it. A
	// nil hook falls back to time.Now.
	Now func() time.Time
}

// NewLab returns a LabGenerator bound to db; hash supplies the canonical
// workspace state hash (pass the *sim.Service the caller owns).
func NewLab(db *repo.DB, hash WorkspaceHasher) *LabGenerator {
	return &LabGenerator{db: db, hash: hash}
}

// clock returns the current UTC time, honoring the Now hook.
func (g *LabGenerator) clock() time.Time {
	if g.Now != nil {
		return g.Now().UTC()
	}
	return time.Now().UTC()
}

// labSevCounts counts check findings per severity (info findings are free in
// the score formula).
type labSevCounts struct {
	Error   int `json:"error"`
	Warning int `json:"warning"`
	Info    int `json:"info"`
}

// LabReport is the JSON structure of GET /api/lab/report?format=json: the
// workspace snapshot with its canonical state hash, the latest check-run
// findings, the changeset/checkpoint history and the pinned disclaimer.
//
// Score is null when the workspace has no check run yet (nothing was ever
// validated — the report refuses to claim a score it did not compute).
type LabReport struct {
	// GeneratedAt is the report creation time (UTC).
	GeneratedAt time.Time `json:"generatedAt"`
	// EngineVersion is the version of the NetOps Lab engine (domain.AppVersion).
	EngineVersion string `json:"engineVersion"`
	// Workspace is the snapshot of the reported workspace.
	Workspace LabWorkspace `json:"workspace"`
	// Score is the latest check-run score, or null when no run exists.
	Score *int `json:"score"`
	// Checks carries the latest run's finding counts and finding rows.
	Checks LabChecks `json:"checks"`
	// ChangeSets lists the last 10 changesets, newest first.
	ChangeSets []LabChangeSet `json:"changesets"`
	// Checkpoints lists the workspace checkpoints, newest first.
	Checkpoints []LabCheckpoint `json:"checkpoints"`
	// Disclaimer is the pinned synthetic-data disclaimer.
	Disclaimer string `json:"disclaimer"`
}

// LabWorkspace is the workspace block of the lab report.
type LabWorkspace struct {
	// ID is the workspace id.
	ID string `json:"id"`
	// Name is the workspace name.
	Name string `json:"name"`
	// WorkspaceHash is the canonical workspace state hash.
	WorkspaceHash string `json:"workspaceHash"`
	// Counts aggregates the workspace inventory.
	Counts LabCounts `json:"counts"`
}

// LabCounts is the inventory aggregate of the reported workspace.
type LabCounts struct {
	Devices     int `json:"devices"`
	UpDevices   int `json:"upDevices"`
	DownDevices int `json:"downDevices"`
	Interfaces  int `json:"interfaces"`
	Links       int `json:"links"`
	UpLinks     int `json:"upLinks"`
	ChangeSets  int `json:"changesets"`
	Checkpoints int `json:"checkpoints"`
}

// LabChecks is the latest check-run block of the lab report: finding counts
// plus the finding rows themselves (baselines are omitted — a report lists
// problems, and a clean check renders as empty results).
type LabChecks struct {
	Counts  labSevCounts `json:"counts"`
	Results []LabFinding `json:"results"`
}

// LabFinding is one check finding of the reported latest run.
type LabFinding struct {
	CheckCode string          `json:"checkCode"`
	Severity  string          `json:"severity"`
	Message   string          `json:"message"`
	Detail    json.RawMessage `json:"detail"`
}

// LabChangeSet is one row of the report's changeset history.
type LabChangeSet struct {
	ID          string     `json:"id"`
	Description string     `json:"description"`
	Trigger     string     `json:"trigger"`
	Status      string     `json:"status"`
	OpsApplied  int        `json:"opsApplied"`
	CreatedAt   time.Time  `json:"createdAt"`
	RevertedAt  *time.Time `json:"revertedAt"`
}

// LabCheckpoint is one row of the report's checkpoint list.
type LabCheckpoint struct {
	CheckpointID  string    `json:"checkpointId"`
	Label         string    `json:"label"`
	WorkspaceHash string    `json:"workspaceHash"`
	CreatedAt     time.Time `json:"createdAt"`
}

// JSON builds the LabReport of the workspace. An unknown workspace yields
// an error wrapping domain.ErrNotFound (the API answers 404).
func (g *LabGenerator) JSON(ctx context.Context, workspaceID string) (*LabReport, error) {
	w, err := g.db.Workspaces.GetByID(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("report: lab workspace %s: %w", workspaceID, err)
	}
	devCounts, err := g.db.VirtualDevices.CountByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("report: count devices of %s: %w", workspaceID, err)
	}
	ifaceCounts, err := g.db.SimInterfaces.CountByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("report: count interfaces of %s: %w", workspaceID, err)
	}
	linkCounts, err := g.db.SimLinks.CountByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("report: count links of %s: %w", workspaceID, err)
	}
	csCount, err := g.db.ChangeSets.CountByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("report: count changesets of %s: %w", workspaceID, err)
	}
	cpCount, err := g.db.Checkpoints.CountByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("report: count checkpoints of %s: %w", workspaceID, err)
	}
	hash, err := g.hash.WorkspaceHash(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("report: workspace hash of %s: %w", workspaceID, err)
	}

	rep := &LabReport{
		GeneratedAt:   g.clock(),
		EngineVersion: domain.AppVersion,
		Workspace: LabWorkspace{
			ID:            w.ID,
			Name:          w.Name,
			WorkspaceHash: hash,
			Counts: LabCounts{
				Devices:     devCounts[domain.EntityStatusUp] + devCounts[domain.EntityStatusDown],
				UpDevices:   devCounts[domain.EntityStatusUp],
				DownDevices: devCounts[domain.EntityStatusDown],
				Interfaces:  ifaceCounts[domain.EntityStatusUp] + ifaceCounts[domain.EntityStatusDown],
				Links:       linkCounts[domain.EntityStatusUp] + linkCounts[domain.EntityStatusDown],
				UpLinks:     linkCounts[domain.EntityStatusUp],
				ChangeSets:  csCount,
				Checkpoints: cpCount,
			},
		},
		ChangeSets:  make([]LabChangeSet, 0, labReportChangeSetLimit),
		Checkpoints: make([]LabCheckpoint, 0, 4),
		Disclaimer:  LabDisclaimer,
	}

	// Latest check run: score + findings. No run yet ⇒ score null, zero
	// counts, empty results (never a fabricated score).
	run, err := g.db.CheckRuns.Latest(ctx, workspaceID)
	switch {
	case err == nil:
		results, rerr := g.db.CheckRuns.GetResults(ctx, run.ID)
		if rerr != nil {
			return nil, fmt.Errorf("report: results of run %s: %w", run.ID, rerr)
		}
		findings := make([]LabFinding, 0, len(results))
		var counts labSevCounts
		for _, r := range results {
			if r.Passed {
				continue
			}
			counts.add(r.Severity)
			findings = append(findings, LabFinding{
				CheckCode: r.CheckCode,
				Severity:  r.Severity.String(),
				Message:   r.Message,
				Detail:    json.RawMessage(r.DetailJSON),
			})
		}
		score := run.Score
		rep.Score = &score
		rep.Checks = LabChecks{Counts: counts, Results: findings}
	case errors.Is(err, domain.ErrNotFound):
		rep.Checks = LabChecks{Results: []LabFinding{}}
	default:
		return nil, fmt.Errorf("report: latest run of %s: %w", workspaceID, err)
	}

	// Changeset history: the last 10, newest first (repo numeric-id order).
	css, _, err := g.db.ChangeSets.ListByWorkspace(ctx, workspaceID, 1, labReportChangeSetLimit)
	if err != nil {
		return nil, fmt.Errorf("report: changesets of %s: %w", workspaceID, err)
	}
	for _, cs := range css {
		item := LabChangeSet{
			ID:          cs.ID,
			Description: cs.Description,
			Trigger:     cs.Trigger.String(),
			Status:      cs.Status.String(),
			OpsApplied:  cs.OpsApplied,
			CreatedAt:   cs.CreatedAt,
		}
		if cs.Reverted() {
			at := cs.RevertedAt
			item.RevertedAt = &at
		}
		rep.ChangeSets = append(rep.ChangeSets, item)
	}

	// Checkpoints, newest first.
	cps, err := g.db.Checkpoints.ListByWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("report: checkpoints of %s: %w", workspaceID, err)
	}
	for _, cp := range cps {
		rep.Checkpoints = append(rep.Checkpoints, LabCheckpoint{
			CheckpointID:  cp.ID,
			Label:         cp.Label,
			WorkspaceHash: cp.WorkspaceHash,
			CreatedAt:     cp.CreatedAt,
		})
	}
	return rep, nil
}

// add folds one finding severity into the counts.
func (c *labSevCounts) add(sev domain.Severity) {
	switch sev {
	case domain.SeverityError:
		c.Error++
	case domain.SeverityWarning:
		c.Warning++
	case domain.SeverityInfo:
		c.Info++
	}
}

// RenderHTML builds the lab report of the workspace and renders it as a
// single self-contained HTML document (inline CSS, no external resources,
// all dynamic values auto-escaped by html/template). The device inventory
// table is loaded here — the JSON contract intentionally carries only
// aggregates. An unknown workspace yields an error wrapping
// domain.ErrNotFound.
func (g *LabGenerator) RenderHTML(ctx context.Context, workspaceID string) (string, error) {
	rep, err := g.JSON(ctx, workspaceID)
	if err != nil {
		return "", err
	}
	devices, err := g.db.VirtualDevices.ListByWorkspace(ctx, workspaceID)
	if err != nil {
		return "", fmt.Errorf("report: devices of %s: %w", workspaceID, err)
	}
	ifaces, err := g.db.SimInterfaces.ListByWorkspace(ctx, workspaceID)
	if err != nil {
		return "", fmt.Errorf("report: interfaces of %s: %w", workspaceID, err)
	}
	ifaceCountByDevice := make(map[string]int, len(ifaces))
	for _, i := range ifaces {
		ifaceCountByDevice[i.DeviceID]++
	}

	var sb strings.Builder
	if err := labTmpl.Execute(&sb, buildLabHTMLView(rep, devices, ifaceCountByDevice)); err != nil {
		return "", fmt.Errorf("report: render lab html: %w", err)
	}
	return sb.String(), nil
}

// ---- HTML view model (all values pre-rendered; the template is logic-free) ----

// labHTMLView is the template data model of the lab report HTML.
type labHTMLView struct {
	WorkspaceName   string
	WorkspaceID     string
	WorkspaceHash   string
	GeneratedAt     string
	EngineVersion   string
	DeviceCount     int
	UpDeviceCount   int
	DownDeviceCount int
	InterfaceCount  int
	LinkCount       int
	UpLinkCount     int
	DownLinkCount   int
	ChangeSetCount  int
	CheckpointCount int
	Score           string // "95" or "—"
	ScoreClass      string
	ErrorCount      int
	WarningCount    int
	InfoCount       int
	Devices         []labDeviceView
	Groups          []labSeverityGroup
	ChangeSets      []labChangeSetView
	Checkpoints     []labCheckpointView
}

// labDeviceView is one row of the device inventory table.
type labDeviceView struct {
	Code           string
	Name           string
	StatusLabel    string // 正常 / 停用
	StatusClass    string // chip-status / chip-error
	InterfaceCount int
}

// labSeverityGroup is one severity section of the findings list (reuses the
// Phase 1 severityMeta labels/chips).
type labSeverityGroup struct {
	Severity string
	Label    string // 错误 / 警告 / 提示
	Class    string // chip-error / chip-warning / chip-info
	Count    int
	Findings []labFindingView
}

// labFindingView is one row of a severity section.
type labFindingView struct {
	CheckCode string
	Message   string
	Detail    string // compact JSON
}

// labChangeSetView is one row of the changeset history table.
type labChangeSetView struct {
	ID           string
	Description  string
	Trigger      string
	TriggerLabel string // 用户操作 / 撤销 / 检查点恢复
	Status       string
	StatusLabel  string // 已应用 / 已撤销 / 失败
	StatusClass  string
	OpsApplied   int
	CreatedAt    string
	RevertedAt   string // "—" or the formatted timestamp
}

// labCheckpointView is one row of the checkpoint table.
type labCheckpointView struct {
	ID        string
	Label     string
	Hash      string // shortened
	CreatedAt string
}

// labTriggerLabels maps changeset triggers to Chinese display labels
// (PROJECT_BRIEF_P2 §10 wording).
var labTriggerLabels = map[string]string{
	domain.ChangeSetTriggerUser.String():              "用户操作",
	domain.ChangeSetTriggerUndo.String():              "撤销",
	domain.ChangeSetTriggerCheckpointRestore.String(): "检查点恢复",
}

// labStatusLabels maps changeset statuses to Chinese display labels.
var labStatusLabels = map[string]string{
	domain.ChangeSetStatusApplied.String():  "已应用",
	domain.ChangeSetStatusReverted.String(): "已撤销",
	domain.ChangeSetStatusFailed.String():   "失败",
}

// labStatusClasses maps changeset statuses to chip classes.
var labStatusClasses = map[string]string{
	domain.ChangeSetStatusApplied.String():  "chip-status",
	domain.ChangeSetStatusReverted.String(): "chip-info",
	domain.ChangeSetStatusFailed.String():   "chip-error",
}

// buildLabHTMLView derives the lab report view model from the built report,
// the device inventory and the per-device interface counts. Devices arrive
// code-sorted from the repository (ListByWorkspace order).
func buildLabHTMLView(rep *LabReport, devices []domain.VirtualDevice, ifaceCountByDevice map[string]int) labHTMLView {
	c := rep.Workspace.Counts
	v := labHTMLView{
		WorkspaceName:   rep.Workspace.Name,
		WorkspaceID:     rep.Workspace.ID,
		WorkspaceHash:   rep.Workspace.WorkspaceHash,
		GeneratedAt:     rep.GeneratedAt.Format("2006-01-02 15:04:05 UTC"),
		EngineVersion:   rep.EngineVersion,
		DeviceCount:     c.Devices,
		UpDeviceCount:   c.UpDevices,
		DownDeviceCount: c.DownDevices,
		InterfaceCount:  c.Interfaces,
		LinkCount:       c.Links,
		UpLinkCount:     c.UpLinks,
		DownLinkCount:   c.Links - c.UpLinks,
		ChangeSetCount:  c.ChangeSets,
		CheckpointCount: c.Checkpoints,
		ErrorCount:      rep.Checks.Counts.Error,
		WarningCount:    rep.Checks.Counts.Warning,
		InfoCount:       rep.Checks.Counts.Info,
	}

	if rep.Score == nil {
		v.Score = "—"
		v.ScoreClass = "muted"
	} else {
		v.Score = strconv.Itoa(*rep.Score)
		switch {
		case *rep.Score >= 80:
			v.ScoreClass = "score-good"
		case *rep.Score >= 50:
			v.ScoreClass = "score-mid"
		default:
			v.ScoreClass = "score-bad"
		}
	}

	v.Devices = make([]labDeviceView, 0, len(devices))
	for _, d := range devices {
		statusLabel, statusClass := "停用", "chip-error"
		if d.Status == domain.EntityStatusUp {
			statusLabel, statusClass = "正常", "chip-status"
		}
		v.Devices = append(v.Devices, labDeviceView{
			Code:           d.Code,
			Name:           d.Name,
			StatusLabel:    statusLabel,
			StatusClass:    statusClass,
			InterfaceCount: ifaceCountByDevice[d.ID],
		})
	}

	for _, meta := range severityMeta {
		group := labSeverityGroup{Severity: meta.severity, Label: meta.label, Class: meta.class}
		for _, f := range rep.Checks.Results {
			if f.Severity != meta.severity {
				continue
			}
			group.Findings = append(group.Findings, labFindingView{
				CheckCode: f.CheckCode,
				Message:   f.Message,
				Detail:    compactJSON(f.Detail),
			})
		}
		group.Count = len(group.Findings)
		v.Groups = append(v.Groups, group)
	}

	v.ChangeSets = make([]labChangeSetView, 0, len(rep.ChangeSets))
	for _, cs := range rep.ChangeSets {
		triggerLabel := labTriggerLabels[cs.Trigger]
		if triggerLabel == "" {
			triggerLabel = cs.Trigger
		}
		revertedAt := "—"
		if cs.RevertedAt != nil {
			revertedAt = cs.RevertedAt.Format("2006-01-02 15:04:05")
		}
		v.ChangeSets = append(v.ChangeSets, labChangeSetView{
			ID:           cs.ID,
			Description:  cs.Description,
			Trigger:      cs.Trigger,
			TriggerLabel: triggerLabel,
			Status:       cs.Status,
			StatusLabel:  labStatusLabels[cs.Status],
			StatusClass:  labStatusClasses[cs.Status],
			OpsApplied:   cs.OpsApplied,
			CreatedAt:    cs.CreatedAt.Format("2006-01-02 15:04:05"),
			RevertedAt:   revertedAt,
		})
	}

	v.Checkpoints = make([]labCheckpointView, 0, len(rep.Checkpoints))
	for _, cp := range rep.Checkpoints {
		hash := cp.WorkspaceHash
		if len(hash) > 12 {
			hash = hash[:12] + "…"
		}
		v.Checkpoints = append(v.Checkpoints, labCheckpointView{
			ID:        cp.CheckpointID,
			Label:     cp.Label,
			Hash:      hash,
			CreatedAt: cp.CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}
	return v
}

// compactJSON renders a raw JSON value as compact text (findings detail).
func compactJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "—"
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}
