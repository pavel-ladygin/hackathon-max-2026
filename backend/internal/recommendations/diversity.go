package recommendations

// diversify preserves the complete score/HMAC order unless an earlier unseen
// primary category can be selected without exceeding the presentation caps.
func diversify(ranked []rankedEvent) []rankedEvent {
	limit := len(ranked)
	if limit > poolTarget {
		limit = min(limit, poolMax)
	}
	return diversifyToLimit(ranked, limit)
}

func diversifyToLimit(ranked []rankedEvent, limit int) []rankedEvent {
	selected := make([]rankedEvent, 0, limit)
	used := make([]bool, len(ranked))
	distinct := make(map[string]bool)
	for len(selected) < limit {
		wantNew := len(distinct) < 4 && hasUnseenPrimary(ranked, used, distinct)
		index := chooseDiverse(ranked, used, selected, distinct, wantNew, true, true)
		// A category-discovery preference never overrides a candidate that obeys
		// both caps. Relax one cap only after no capped candidate remains.
		if index < 0 && wantNew {
			index = chooseDiverse(ranked, used, selected, distinct, false, true, true)
		}
		if index < 0 {
			index = chooseDiverse(ranked, used, selected, distinct, wantNew, true, false)
		}
		if index < 0 && wantNew {
			index = chooseDiverse(ranked, used, selected, distinct, false, true, false)
		}
		if index < 0 {
			index = chooseDiverse(ranked, used, selected, distinct, wantNew, false, true)
		}
		if index < 0 && wantNew {
			index = chooseDiverse(ranked, used, selected, distinct, false, false, true)
		}
		if index < 0 {
			index = chooseDiverse(ranked, used, selected, distinct, wantNew, false, false)
		}
		if index < 0 && wantNew {
			index = chooseDiverse(ranked, used, selected, distinct, false, false, false)
		}
		if index < 0 {
			break
		}
		used[index] = true
		selected = append(selected, ranked[index])
		if ranked[index].primary != "" {
			distinct[ranked[index].primary] = true
		}
	}
	return selected
}

func hasUnseenPrimary(ranked []rankedEvent, used []bool, selected map[string]bool) bool {
	for i, candidate := range ranked {
		if !used[i] && candidate.primary != "" && !selected[candidate.primary] {
			return true
		}
	}
	return false
}

func chooseDiverse(ranked []rankedEvent, used []bool, selected []rankedEvent, categories map[string]bool, wantNew, categoryCapped, venueCapped bool) int {
	for i, candidate := range ranked {
		if used[i] || (wantNew && (candidate.primary == "" || categories[candidate.primary])) {
			continue
		}
		if respectsCaps(candidate, selected, categoryCapped, venueCapped) {
			return i
		}
	}
	return -1
}

func respectsCaps(candidate rankedEvent, selected []rankedEvent, categoryCapped, venueCapped bool) bool {
	if venueCapped && len(selected) >= 2 && candidate.venue.ID == selected[len(selected)-1].venue.ID && candidate.venue.ID == selected[len(selected)-2].venue.ID {
		return false
	}
	if !categoryCapped || candidate.primary == "" || len(selected) < 3 {
		return true
	}
	for i := len(selected) - 3; i < len(selected); i++ {
		if selected[i].primary != candidate.primary {
			return true
		}
	}
	return false
}
