package storage

// Match implements byte-oriented Redis-style glob matching. The dynamic program
// uses O(len(value)) memory and avoids exponential recursive wildcard matching.
func Match(pattern, value string) bool {
	prev := make([]bool, len(value)+1)
	prev[0] = true
	for i := 0; i < len(pattern); {
		c := pattern[i]
		i++
		next := make([]bool, len(prev))
		if c == '*' {
			next[0] = prev[0]
			for j := 1; j < len(next); j++ {
				next[j] = prev[j] || next[j-1]
			}
			prev = next
			continue
		}
		var chars [256]bool
		if c == '?' {
			for j := range chars {
				chars[j] = true
			}
		} else if c == '[' {
			neg := i < len(pattern) && pattern[i] == '^'
			if neg {
				i++
			}
			for i < len(pattern) && pattern[i] != ']' {
				a := pattern[i]
				i++
				if a == '\\' && i < len(pattern) {
					a = pattern[i]
					i++
				}
				b := a
				if i+1 < len(pattern) && pattern[i] == '-' && pattern[i+1] != ']' {
					i++
					b = pattern[i]
					i++
					if a > b {
						a, b = b, a
					}
				}
				for n := int(a); n <= int(b); n++ {
					chars[n] = true
				}
			}
			if i == len(pattern) {
				return false
			}
			i++
			if neg {
				for j := range chars {
					chars[j] = !chars[j]
				}
			}
		} else {
			if c == '\\' && i < len(pattern) {
				c = pattern[i]
				i++
			}
			chars[c] = true
		}
		for j := 1; j < len(next); j++ {
			next[j] = prev[j-1] && chars[value[j-1]]
		}
		prev = next
	}
	return prev[len(value)]
}
