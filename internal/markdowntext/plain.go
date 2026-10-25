package markdowntext

import (
	"strings"

	blackfriday "github.com/russross/blackfriday/v2"
)

// Plain converts Markdown into readable notification text. The original
// Markdown is kept separately so clients that support rich text can render it.
func Plain(source string) string {
	root := blackfriday.New(blackfriday.WithExtensions(blackfriday.CommonExtensions &^ blackfriday.NoIntraEmphasis)).Parse([]byte(source))
	var out strings.Builder
	root.Walk(func(node *blackfriday.Node, entering bool) blackfriday.WalkStatus {
		if entering {
			switch node.Type {
			case blackfriday.Text, blackfriday.Code, blackfriday.CodeBlock:
				out.Write(node.Literal)
			case blackfriday.Softbreak, blackfriday.Hardbreak:
				out.WriteByte('\n')
			case blackfriday.Item:
				out.WriteString("• ")
			case blackfriday.HTMLBlock, blackfriday.HTMLSpan:
				// Raw HTML is not displayed inside a system notification.
			case blackfriday.Image:
				// Images cannot be embedded in notification text; keep the URL.
				out.WriteString(string(node.Destination))
				return blackfriday.SkipChildren
			}
		} else {
			switch node.Type {
			case blackfriday.Paragraph, blackfriday.Heading, blackfriday.Item, blackfriday.CodeBlock,
				blackfriday.BlockQuote, blackfriday.TableRow:
				out.WriteByte('\n')
			case blackfriday.Link:
				// Preserve the destination when link text alone would lose the action.
				if len(node.Destination) > 0 {
					out.WriteString(" (")
					out.Write(node.Destination)
					out.WriteByte(')')
				}
			}
		}
		return blackfriday.GoToNext
	})
	lines := strings.Split(out.String(), "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n")
}
