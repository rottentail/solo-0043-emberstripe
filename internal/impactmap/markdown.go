package impactmap

import (
	"fmt"
	"strings"
)

// RenderMarkdown renders a colleague-facing handoff note.  It is intentionally
// redundant with the JSON so the page-level lineage survives copy/paste.
func RenderMarkdown(m *ImpactMap) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# 撕边影响图 %s\n\n", m.Schema)
	fmt.Fprintf(&b, "- 调查摘要：`%s`\n", m.SurveySHA256[:16])
	fmt.Fprintf(&b, "- 原扫：`%s`，声画偏移 %d 行，%d 格 × %d 行，卷摘要 `%s`\n",
		m.Original.Name, m.Original.LeadRows, m.Original.Frames, m.Original.RowsPerFrame, short(m.Original.SHA256))
	fmt.Fprintf(&b, "- 补扫：`%s`，声画偏移 %d 行，%d 格 × %d 行，卷摘要 `%s`\n\n",
		m.Rescan.Name, m.Rescan.LeadRows, m.Rescan.Frames, m.Rescan.RowsPerFrame, short(m.Rescan.SHA256))
	b.WriteString("## 证据边界\n\n")
	for _, policy := range m.LineagePolicy {
		fmt.Fprintf(&b, "- %s\n", policy)
	}
	b.WriteString("\n")

	for _, seg := range m.Segments {
		fmt.Fprintf(&b, "## 伤痕 %s\n\n", seg.ID)
		if seg.Note != "" {
			fmt.Fprintf(&b, "%s\n\n", seg.Note)
		}
		if len(seg.CorroboratedRanges) > 0 {
			b.WriteString("可由参照和补扫页共同证实的原扫行：")
			for _, r := range seg.CorroboratedRanges {
				fmt.Fprintf(&b, " `%s`", formatAbs(m.Original, r))
			}
			b.WriteString("\n\n")
		} else {
			b.WriteString("没有可完整证实的交叉扫描区间；下列行仍须回原扫/原片确认。\n\n")
		}
		if seg.ConfirmOriginalFrom != nil {
			fmt.Fprintf(&b, "**从原扫 %s（绝对行 %d）起，后段必须回原扫确认。**\n\n",
				formatBoundary(*seg.ConfirmOriginalFrom), seg.ConfirmOriginalFrom.AbsoluteRow)
		}
		if seg.ConfirmOriginalBefore != nil {
			fmt.Fprintf(&b, "在原扫 %s 之前的首段未获交叉证实。\n\n", formatBoundary(*seg.ConfirmOriginalBefore))
		}
		renderSideMarkdown(&b, m.Original, seg.Original)
		if seg.Rescan != nil {
			renderSideMarkdown(&b, m.Rescan, *seg.Rescan)
		} else {
			b.WriteString("### 补扫\n\n未能投影：缺少双方认定的参照位置。\n\n")
		}
		if len(seg.FollowUps) > 0 {
			b.WriteString("### 待确认与回查\n\n")
			for _, f := range seg.FollowUps {
				fmt.Fprintf(&b, "- `%s` %s–%s：%s（%s）\n", f.Reel, formatBoundary(f.From), formatBoundary(f.To), f.Reason, f.Kind)
			}
			b.WriteString("\n")
		}
	}
	if len(m.Warnings) > 0 {
		b.WriteString("## 警告\n\n")
		for _, w := range m.Warnings {
			fmt.Fprintf(&b, "- %s\n", w)
		}
	}
	return []byte(b.String())
}

func renderSideMarkdown(b *strings.Builder, summary ReelSummary, side SideReport) {
	fmt.Fprintf(b, "### %s：`%s`\n\n", sideTitle(side.Side), side.Reel)
	fmt.Fprintf(b, "- 扫描行：`%s` 至 `%s`（绝对行 %d–%d，半开）\n",
		formatBoundary(side.Start), formatBoundary(side.End), side.ScanRange.Start, side.ScanRange.End)
	fmt.Fprintf(b, "- 列区间：x=%d:%d（%s）\n", side.Columns.X0, side.Columns.X1, side.ColumnBasis)
	if len(side.LaneHits) == 0 {
		b.WriteString("- 未碰到声明的左/右声槽。\n")
	} else {
		for _, lane := range side.LaneHits {
			fmt.Fprintf(b, "- 碰到%s声槽（x=%d:%d）；实际可复听画格行 %d 行", laneNameCN(lane.Lane), lane.X0, lane.X1, lane.AudibleRowCount)
			if len(lane.AudibleFrameRuns) > 0 {
				b.WriteString("：")
				for _, r := range lane.AudibleFrameRuns {
					fmt.Fprintf(b, " `%s`", formatFrameRange(summary, r))
				}
			}
			b.WriteString("\n")
			for _, run := range lane.FrameRuns {
				fmt.Fprintf(b, "  - 画格行 %s，状态 `%s`", formatFrameRange(summary, AbsRange{run.FrameRowStart, run.FrameRowEnd}), run.Status)
				if run.Reason != "" {
					fmt.Fprintf(b, "（%s）", run.Reason)
				}
				fmt.Fprintf(b, "，声源扫描行 %d–%d", run.SourceScanStart, run.SourceScanEnd)
				if len(run.Evidence) > 0 {
					b.WriteString("，页证：")
					for _, ev := range run.Evidence {
						if ev.Page < 0 || ev.Reel == "" {
							continue
						}
						if ev.Present && ev.SHA256 != "" {
							fmt.Fprintf(b, " `%s#p%04d:%d-%d:%s`", ev.Reel, ev.Page, ev.StartRow, ev.EndRow, short(ev.SHA256))
						} else {
							fmt.Fprintf(b, " `%s#p%04d缺失`", ev.Reel, ev.Page)
						}
					}
				}
				b.WriteString("\n")
			}
		}
	}
	if len(side.PictureRuns) > 0 {
		b.WriteString("- 画面受影响画格行：")
		for _, r := range side.PictureRuns {
			fmt.Fprintf(b, " `%s`", formatFrameRange(summary, AbsRange{r.FrameRowStart, r.FrameRowEnd}))
		}
		b.WriteString("\n")
	}
	if len(side.PictureOnlyRuns) > 0 {
		b.WriteString("- **只有画面受影响**：")
		for _, r := range side.PictureOnlyRuns {
			fmt.Fprintf(b, " `%s`", formatFrameRange(summary, AbsRange{r.FrameRowStart, r.FrameRowEnd}))
		}
		b.WriteString("\n")
	}
	if side.AuditionURL != "" {
		fmt.Fprintf(b, "- 试听入口：`%s`（仅整格窗口，最多 48 格）\n", side.AuditionURL)
	}
	b.WriteString("\n")
}

func sideTitle(side string) string {
	if side == "original" {
		return "原扫"
	}
	return "补扫"
}

func laneNameCN(lane int) string {
	if lane == 0 {
		return "左"
	}
	return "右"
}

func short(digest string) string {
	if len(digest) <= 12 {
		return digest
	}
	return digest[:12]
}

func formatBoundary(b Boundary) string {
	if b.Page < 0 {
		return fmt.Sprintf("卷外/%d", b.AbsoluteRow)
	}
	return fmt.Sprintf("p%04d#%d", b.Page, b.Row)
}

func formatAbs(h ReelSummary, r AbsRange) string {
	return formatBoundary(Boundary{Page: r.Start / h.RowsPerFrame, Row: r.Start % h.RowsPerFrame, AbsoluteRow: r.Start}) +
		"…" + formatBoundary(Boundary{Page: r.End / h.RowsPerFrame, Row: r.End % h.RowsPerFrame, AbsoluteRow: r.End})
}

func formatFrameRange(h ReelSummary, r AbsRange) string {
	start := Boundary{AbsoluteRow: r.Start}
	if r.Start >= 0 {
		start.Page = r.Start / h.RowsPerFrame
		start.Row = r.Start % h.RowsPerFrame
	}
	end := Boundary{AbsoluteRow: r.End}
	if r.End >= 0 {
		end.Page = r.End / h.RowsPerFrame
		end.Row = r.End % h.RowsPerFrame
	}
	return fmt.Sprintf("%s–%s（绝对行 %d:%d）", formatBoundary(start), formatBoundary(end), r.Start, r.End)
}
