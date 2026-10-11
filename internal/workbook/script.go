// PTP configuration-script channel (v0.5 W8-D): .txt/.cfg files carrying
// the PTP script fingerprint (see ptpconfig.IsScriptText) are parsed by
// the pure ptpconfig parser and synthesized into ONE virtual sheet, so the
// whole downstream pipeline — column suggestions, mapping confirmation,
// import execution — is byte-identical to the xlsx flow. The xlsx path of
// Read is untouched: only the .txt/.cfg extension enters the script branch.
package workbook

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mmqz/netops-lab/internal/domain"
	"github.com/mmqz/netops-lab/internal/mapping"
	"github.com/mmqz/netops-lab/internal/ptpconfig"
)

// ErrUnsupportedScript is returned (wrapped) when a .txt/.cfg upload does
// not carry the PTP script fingerprint: the extension asked for the script
// channel, but the content is neither an xlsx workbook nor a recognizable
// PTP configuration script. The HTTP layer maps it to 422
// unsupported_script.
var ErrUnsupportedScript = errors.New("workbook: not a supported PTP configuration script")

// scriptExtensions are the extensions routed to the script channel (the
// content sniff must still pass; any other extension keeps the xlsx path
// and its error semantics unchanged).
var scriptExtensions = map[string]bool{
	".txt": true,
	".cfg": true,
}

// Read parses the .xlsx file at path, or — for a .txt/.cfg path whose
// content carries the PTP script fingerprint — parses the PTP
// configuration script and synthesizes the virtual workbook. Script-path
// errors: ErrUnsupportedScript (fingerprint absent), ptpconfig.ErrSyntax
// (malformed script, line-numbered) and ErrEmptyWorkbook (sections without
// any interface row); the xlsx path's error semantics are byte-identical
// to the pre-W8-D behaviour. Read itself lives in workbook.go; this file
// only contributes the script branch's helpers.
func scriptWorkbook(path string, data []byte) (*Workbook, error) {
	if !ptpconfig.IsScriptText(data) {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedScript, filepath.Base(path))
	}
	sections, err := ptpconfig.Parse(data)
	if err != nil {
		return nil, err // wraps ptpconfig.ErrSyntax (line-numbered)
	}
	wb, err := synthSheet(sections)
	if err != nil {
		return nil, err
	}
	wb.Name = filepath.Base(path)
	return wb, nil
}

// synthSheet builds the single virtual sheet from the parsed sections.
//
//   - Sheet name: ptpconfig.ScriptSheetName ("PTP配置") — its "PTP"
//     substring is ptp_port's SheetHint, so SuggestType proposes ptp_port
//     and SuggestMapping prefills the full mapping.
//   - ONE sheet (not one per model), with the model as the 设备型号 COLUMN:
//     all rows share the same column schema, the composite natural key
//     (device_model|port_name) needs the model as a mappable column
//     anyway, and one sheet keeps the analyze → confirm → import flow and
//     the suggested mapping identical to xlsx (per-model sheets would
//     duplicate the same 23 columns once per model).
//   - Headers: the ptp_port spec fields in canonical order, named by each
//     field's Label (also the field's first synonym) — the suggestion
//     engine matches every column at synonym confidence (0.75).
//   - Row numbers: the REAL 1-based script line number of the port's
//     creating line (interface or free-mode port line), so issue evidence,
//     row results and source refs point back into the script.
func synthSheet(sections []ptpconfig.ScriptSection) (*Workbook, error) {
	spec, ok := mapping.SpecByType(domain.ResourceTypePTPPort)
	if !ok {
		return nil, fmt.Errorf("workbook: ptp_port spec missing from mapping.Specs")
	}
	headers := make([]string, len(spec.Fields))
	for i, f := range spec.Fields {
		headers[i] = f.Label
	}
	sheet := Sheet{Name: ptpconfig.ScriptSheetName, Headers: headers}
	total := 0
	for _, s := range sections {
		for _, p := range s.Ports {
			// Flat honest row: the identity fields (device_model, port_name,
			// annotation) from the entry itself, overlaid by the block-global
			// parameters (repeated per row of the model) and the port's own
			// parameters.
			vals := map[string]string{
				"device_model": s.Model,
				"port_name":    p.PortName,
				"annotation":   p.Annotation,
			}
			for k, v := range s.Globals {
				vals[k] = v
			}
			for k, v := range p.Params {
				vals[k] = v
			}
			row := Row{Number: p.Line, Values: make(map[string]string, len(headers))}
			for i, f := range spec.Fields {
				row.Values[headers[i]] = vals[f.Name]
			}
			sheet.Rows = append(sheet.Rows, row)
			total++
			if MaxRowsPerSheet > 0 && total > MaxRowsPerSheet {
				return nil, fmt.Errorf("%w: script synthesizes more than %d port rows",
					ErrWorkbookTooLarge, MaxRowsPerSheet)
			}
		}
	}
	if total == 0 {
		return nil, fmt.Errorf("%w: PTP 脚本不含任何接口行", ErrEmptyWorkbook)
	}
	sheet.DataRows = total
	return &Workbook{Sheets: []Sheet{sheet}}, nil
}

// isScriptExtension reports whether ext routes to the script channel.
func isScriptExtension(ext string) bool {
	return scriptExtensions[strings.ToLower(ext)]
}
