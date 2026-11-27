package diffreview

import (
	"encoding/json"
	"fmt"
	"strings"
)

// RenderMarkdown renders the desk-facing Chinese difference listening report.
func RenderMarkdown(rep *Report) string {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	w("# 离线差异听审报告")
	w("")
	w("- 契约：`%s`", rep.Schema)
	w("- 生成时间：%s", orDash(rep.GeneratedAt))
	w("- 性质：离线对照两个**已校验冻结交接包**（parcel ZIP），不访问当前扫描卷。")
	w("- 逐画格标记取值：**内容变化**（`content_change`，有证据的声学变化）、**可读性变化**（`readability_change`，可确认的覆盖变化）、**无法对位**（`unaligned`，网格不同且证据不足，保留两个原包可复听位置）、`same`（一致，不抽取）。")
	w("")

	w("## 1. 两个原包的出处（provenance）")
	w("")
	w("| 角色 | 文件 | 包 ID = ZIP SHA-256 | 卷名 capture | source_sha256 | audio_sha256 | 发布画格窗 | fps | 每格行数 | 采样率 | lead | 标定 | 字节 | 校验 |")
	w("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |")
	for _, s := range rep.Sources {
		w("| %s | `%s` | `%s` | `%s` | `%s` | `%s` | [%d,%d) | %d | %d | %d | %d | `%s` | %d | %v |",
			strings.ToUpper(s.Role), orDash(s.GivenPath), s.ParcelID, s.Capture, s.SourceSHA256, s.AudioSHA256,
			s.Window.Start, s.Window.End, s.FPS, s.RowsPerFrame, s.SampleRate, s.LeadRows,
			s.Gauge.Profile, s.Bytes, s.Verified)
	}
	w("")
	w("> 复听时以包 ID 在归档目录定位 `<id>.zip`，解压后跳到 `sound.wav` 的 cue 位置；包 ID 即整包字节 SHA-256，可用 `GET /parcels/{id}/verify` 或本工具复核。")
	w("")

	w("## 2. 时间基与重合窗口")
	w("")
	tb := rep.Timebase
	w("- 画格率：A=%d，B=%d；相同画格率：**%v**。", tb.FPSA, tb.FPSB, tb.SameFPS)
	w("- 每格行数：A=%d，B=%d；网格相同：**%v**。", tb.RowsA, tb.RowsB, tb.SameGrid)
	w("- 对位方式：`%s`；共同计量采样率：%d Hz。", tb.AlignmentBasis, tb.ComparisonRate)
	if rep.Overlap != nil {
		w("- 共同画格范围（半开、零基）：**[%d,%d)**，共 %d 格；连续画格时间 %.3f–%.3f。",
			rep.Overlap.Start, rep.Overlap.End, rep.Overlap.End-rep.Overlap.Start,
			rep.OverlapSpan.FrameStart, rep.OverlapSpan.FrameEnd)
	} else {
		w("- 共同画格范围：**无重合**。")
	}
	for _, n := range rep.Notes {
		w("- 注意：%s", n)
	}
	w("")

	if len(rep.Exclusive) > 0 {
		w("### 2.1 窗口不重合部分（只在一个原包中，不能同格对照）")
		w("")
		w("| 侧 | 独占画格区间 | 格数 | 该包复听 cue（画格:行 / WAV 时间 / PCM 帧 / 字节） |")
		w("| --- | --- | --- | --- |")
		for _, ex := range rep.Exclusive {
			w("| %s | [%d,%d) | %d | %s |", strings.ToUpper(ex.Side), ex.FrameStart, ex.FrameEnd, ex.Frames, cueMD(ex.Cue))
		}
		w("")
	}

	w("## 3. 同一画格位置的标记")
	w("")
	if len(rep.Frames) == 0 {
		w("没有可对照的共同画格。")
		w("")
	} else {
		w("| 画格 | 标记 | 网格 | 覆盖证据 | 声学证据 | A 复听位置 | B 复听位置 | 区域 |")
		w("| --- | --- | --- | --- | --- | --- | --- | --- |")
		for _, f := range rep.Frames {
			cov := "—"
			if len(f.CoverageChanges) > 0 {
				parts := make([]string, 0, len(f.CoverageChanges))
				for _, c := range f.CoverageChanges {
					parts = append(parts, fmt.Sprintf("%s→%s @%.3f–%.3f 格（可听状态改变=%v）",
						c.StatusAZh, c.StatusBZh, c.Span.FrameStart, c.Span.FrameEnd, c.AudibleStateChanged))
				}
				cov = strings.Join(parts, "；")
			}
			aco := "—"
			if len(f.Acoustic) > 0 || len(f.Unaligned) > 0 {
				parts := make([]string, 0)
				for _, a := range f.Acoustic {
					parts = append(parts, acousticBrief(a))
				}
				for _, u := range f.Unaligned {
					parts = append(parts, "无法对位("+u.Reason+")")
				}
				aco = strings.Join(parts, "；")
			}
			grid := "同格"
			if !f.GridAligned {
				grid = "异网格"
			}
			region := orDash(f.RegionID)
			w("| %d | **%s** `%s` | %s | %s | %s | %s | %s | %s |",
				f.Frame, f.MarkZh, f.Mark, grid, cov, aco, cueMD(f.Cues.A), cueMD(f.Cues.B), region)
		}
		w("")
	}

	w("## 4. 抽取的差异听审区域")
	w("")
	if len(rep.Regions) == 0 {
		w("共同画格内没有确认的差异或对位不足区间：两个包在可对照位置一致（或仅有等价覆盖状态）。`review.wav` 为空节目。")
		w("")
	}
	for _, r := range rep.Regions {
		w("### %s · %s（画格 %s）", r.ID, r.MarkZh, intsJoin(r.Frames))
		w("")
		w("- 标记码：`%s`；同区其它标记：%s", r.Mark, marksJoin(r.AlsoMarks))
		w("- 精确区间：画格时间 %.6f–%.6f（%.4f 格 / %.3f 秒）；加手柄片段区间 %.4f–%.4f。",
			r.Span.FrameStart, r.Span.FrameEnd, r.Span.DurationFrames, r.Span.DurationSeconds,
			r.ClipSpan.FrameStart, r.ClipSpan.FrameEnd)
		w("- 听审文件：对照 `%s`（A→间隔→B，%d Hz）；A 原速摘录 `%s`；B 原速摘录 `%s`；总节目 `review.wav`。",
			orDash(r.Files.CompareWAV), rep.Timebase.ComparisonRate, orDash(r.Files.AWAV), orDash(r.Files.BWAV))
		w("- A 包复听：%s", cueMD(r.Cues.A))
		w("- B 包复听：%s", cueMD(r.Cues.B))
		w("- 提示：%s", r.ListeningHintZh)
		if len(r.CoverageEvidence) > 0 {
			w("- 覆盖变化证据（与声学分开）：")
			for _, c := range r.CoverageEvidence {
				w("    - 画格 %d，%.6f–%.6f 格：`%s`(%s) → `%s`(%s)；可听状态改变=%v；依据=%s",
					c.Frame, c.Span.FrameStart, c.Span.FrameEnd, c.StatusA, c.StatusAZh, c.StatusB, c.StatusBZh,
					c.AudibleStateChanged, c.Confirmation)
			}
		}
		if len(r.AcousticEvidence) > 0 {
			w("- 声学变化证据（有计量）：")
			for _, a := range r.AcousticEvidence {
				w("    - 画格 %d，%.6f–%.6f 格：`%s`，比较样本 %d；changed=[%d,%d]；RMSΔ=[%.1f, %.1f]（归一 %.4f/%.4f）；peakΔ=[%.0f, %.0f]（归一 %.4f/%.4f）；结论=`%s`（证据充分=%v）",
					a.Frame, a.Span.FrameStart, a.Span.FrameEnd, a.Basis, a.ComparedFrames,
					a.ChangedSamples[0], a.ChangedSamples[1],
					a.RMSDelta[0], a.RMSDelta[1], a.RMSDeltaNorm[0], a.RMSDeltaNorm[1],
					a.PeakDelta[0], a.PeakDelta[1], a.PeakDeltaNorm[0], a.PeakDeltaNorm[1],
					a.Conclusion, a.EvidenceSufficient)
			}
		}
		if len(r.Unaligned) > 0 {
			w("- 对位不足（保留双包复听位置，不做内容结论）：")
			for _, u := range r.Unaligned {
				w("    - 画格 %d，%.6f–%.6f 格：`%s` —— %s", u.Frame, u.Span.FrameStart, u.Span.FrameEnd, u.Reason, u.Detail)
			}
		}
		w("")
	}

	w("## 5. 统计")
	w("")
	w("```json")
	w(summaryJSON(rep))
	w("```")
	w("")
	w("## 6. 判定策略")
	w("")
	w("- 画格主标记优先级：%s。混合画格的全部覆盖/声学/对位证据仍在 JSON 与上表分行保留。", rep.Policy.FrameMarkPrecedence)
	w("- %s", rep.Policy.NoteZh)
	w("- 阈值：change_rms=%.3f，change_peak=%.3f，same_rms=%.4f，same_peak=%.3f，min_span=%.3fs，pad=%.3fs。",
		rep.Policy.ChangeRMS, rep.Policy.ChangePeak, rep.Policy.SameRMS, rep.Policy.SamePeak,
		rep.Policy.MinSpanSeconds, rep.Policy.PadSeconds)
	w("")
	return b.String()
}

func cueMD(c Cue) string {
	if c.ParcelID == "" {
		return "—"
	}
	return fmt.Sprintf("格 %d 行 %d（picture_row=%d）／`sound.wav!#%d` %.3fs `%s`／字节 %d／包 `%s…`",
		c.Frame, c.RowInFrame, c.PictureRow, c.PCMFrame, c.WAVSeconds, c.WAVTimecode, c.WAVByteOffset, c.ParcelID[:12])
}

func acousticBrief(a AcousticEvidence) string {
	return fmt.Sprintf("%s n=%d rmsΔnorm=[%.4f,%.4f] peakΔnorm=[%.3f,%.3f] → `%s`",
		a.Basis, a.ComparedFrames, a.RMSDeltaNorm[0], a.RMSDeltaNorm[1],
		a.PeakDeltaNorm[0], a.PeakDeltaNorm[1], a.Conclusion)
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func intsJoin(xs []int) string {
	if len(xs) == 0 {
		return "—"
	}
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = fmt.Sprintf("%d", x)
	}
	return strings.Join(parts, ", ")
}

func marksJoin(xs []string) string {
	if len(xs) == 0 {
		return "无"
	}
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = MarkZh[x] + "(`" + x + "`)"
	}
	return strings.Join(parts, "，")
}

func summaryJSON(rep *Report) string {
	b, err := json.MarshalIndent(rep.Summary, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b)
}
