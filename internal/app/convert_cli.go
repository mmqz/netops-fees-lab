package app

// convert_cli.go — the `netopsfees convert` subcommand (issue #7): a
// batch, fidelity-preserving .xls → .xlsx/.csv container swap. The
// original .xls stays untouched (issue #131/#6's read-only source
// discipline); outputs land in --out, results print as the single JSON
// object of the §10 CLI contract.

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/mmqz/netops-fees-lab/internal/convert"
)

// cmdConvert implements `netopsfees convert --in <dir-or-file> --out <dir>
// [--format xlsx|csv] [--recursive]`.
func cmdConvert(args []string, stdout, stderr io.Writer) error {
	fs, usage := newFlagSet("convert")
	in := fs.String("in", "", "输入：一个 .xls 文件，或包含若干 .xls 的目录")
	out := fs.String("out", "./converted", "输出目录（不存在则创建；xlsx 保留原文件名，csv 按 <原名>/<sheet>.csv 分 sheet 落盘）")
	format := fs.String("format", "xlsx", "输出容器：xlsx（默认）或 csv（每 sheet 一个 CSV，多 sheet 台账保持拆分）")
	recursive := fs.Bool("recursive", false, "输入为目录时递归扫描子目录")
	if err := fs.Parse(args); err != nil {
		return &usageError{msg: "参数解析失败：" + err.Error(), usage: usage.String()}
	}
	if fs.NArg() > 0 {
		return &usageError{msg: "convert 不接受位置参数：" + strings.Join(fs.Args(), " "), usage: usage.String()}
	}
	if *in == "" {
		return &usageError{msg: "缺少 --in 参数（.xls 文件或目录）", usage: usage.String()}
	}
	fmtf, err := convert.ParseFormat(*format)
	if err != nil {
		return &usageError{msg: err.Error(), usage: usage.String()}
	}

	res, err := convert.Run(*in, *out, fmtf, *recursive)
	if err != nil {
		return err
	}
	enc, jerr := json.MarshalIndent(res, "", "  ")
	if jerr != nil {
		return jerr
	}
	if _, err := stdout.Write(append(enc, '\n')); err != nil {
		return err
	}
	// Failures are reported in the JSON (never silent) AND reflected in the
	// exit code: a batch with corrupt files answers exitFailure while the
	// single-JSON contract holds (the sentinel tells dispatch the envelope
	// already went out).
	if res.Failed > 0 {
		return fmt.Errorf("%d 个文件转换失败（明细见 stdout JSON）: %w", res.Failed, errAlreadyPrinted)
	}
	return nil
}
