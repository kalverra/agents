package ticket

import (
	"fmt"
	"strings"
)

// FetchPayloadToAIXML renders a ticket fetch result as XML for LLM-friendly parsing (--ai-output).
func FetchPayloadToAIXML(p FetchPayload) string {
	var b strings.Builder
	b.WriteString(`<ticket_fetch status="ok">` + "\n")
	ti := p.Task
	writeAIXMLElement(&b, "  ", "id", ti.ID, false)
	writeAIXMLElement(&b, "  ", "title", ti.Title, false)
	writeAIXMLElement(&b, "  ", "description", ti.Description, true)
	writeAIXMLElement(&b, "  ", "status", ti.Status, true)
	writeAIXMLElement(&b, "  ", "url", ti.URL, true)
	b.WriteString("  <comments>\n")
	for _, c := range p.Comments {
		b.WriteString("    <comment>\n")
		writeAIXMLElement(&b, "      ", "id", c.ID, false)
		writeAIXMLElement(&b, "      ", "posted_at", c.PostedAt, true)
		writeAIXMLElement(&b, "      ", "project_id", c.ProjectID, true)
		writeAIXMLElement(&b, "      ", "content", c.Content, false)
		b.WriteString("    </comment>\n")
	}
	b.WriteString("  </comments>\n")
	b.WriteString("</ticket_fetch>\n")
	return b.String()
}

func writeAIXMLElement(b *strings.Builder, indent, name, val string, omitIfEmpty bool) {
	if omitIfEmpty && strings.TrimSpace(val) == "" {
		return
	}
	b.WriteString(indent)
	b.WriteByte('<')
	b.WriteString(name)
	b.WriteByte('>')
	b.WriteString(xmlTextEscape(val))
	b.WriteString("</")
	b.WriteString(name)
	b.WriteString(">\n")
}

func xmlTextEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}

// TicketToMarkdownFile renders ticket details and comments into a structured XML-markdown string for <KEY>.md files.
//
//nolint:revive
func TicketToMarkdownFile(tk *Ticket) string {
	if tk == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<ticket summary=%q link=%q id=%q status=%q>\n", tk.Title, tk.URL, tk.ID, tk.Status)
	b.WriteString("<details>\n")
	if strings.TrimSpace(tk.Description) != "" {
		b.WriteString(tk.Description)
		b.WriteString("\n")
	}
	b.WriteString("</details>\n")
	b.WriteString("<comments>\n")
	for _, c := range tk.Comments {
		if c.PostedAt != "" {
			fmt.Fprintf(&b, "  <comment time=%q>\n", c.PostedAt)
		} else {
			b.WriteString("  <comment>\n")
		}
		if strings.TrimSpace(c.Content) != "" {
			b.WriteString("  ")
			b.WriteString(c.Content)
			b.WriteString("\n")
		}
		b.WriteString("  </comment>\n")
	}
	b.WriteString("</comments>\n")
	b.WriteString("</ticket>\n")
	return b.String()
}
