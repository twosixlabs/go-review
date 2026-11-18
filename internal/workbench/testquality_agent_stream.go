package workbench

import (
	"fmt"
	"strings"
)

type qualityAgentRepetitionError struct{ paragraphs, bytes int }

func (e *qualityAgentRepetitionError) Error() string {
	return fmt.Sprintf("model generation is repeating the same %d-paragraph text block (%d bytes per repeat)", e.paragraphs, e.bytes)
}

// Inspect prose as it arrives, not tool arguments or a completed response.
// Three adjacent copies of a substantial block indicate a generation loop;
// ordinary recurring headings and boilerplate between distinct findings do not.
type qualityAgentStream struct {
	pending string
	recent  []string
}

func (s *qualityAgentStream) inspect(delta string) error {
	s.pending = strings.ReplaceAll(s.pending+delta, "\r\n", "\n")
	for {
		end := strings.Index(s.pending, "\n\n")
		if end < 0 {
			return nil
		}
		paragraph := strings.TrimSpace(s.pending[:end])
		s.pending = s.pending[end+2:]
		if paragraph == "" {
			continue
		}
		s.recent = append(s.recent, paragraph)
		if len(s.recent) > 12 {
			s.recent = s.recent[len(s.recent)-12:]
		}
		for width := 1; width <= 4 && width*3 <= len(s.recent); width++ {
			tail := s.recent[len(s.recent)-width*3:]
			repeated, size := true, 0
			for i := 0; i < width; i++ {
				size += len(tail[i])
				if tail[i] != tail[i+width] || tail[i] != tail[i+width*2] {
					repeated = false
					break
				}
			}
			if repeated && size >= 160 {
				return &qualityAgentRepetitionError{paragraphs: width, bytes: size}
			}
		}
	}
}
