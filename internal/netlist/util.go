package netlist

import "sort"

func sortStrings(s []string) { sort.Strings(s) }

// uniqueSorted 对后继器件去重并按 id 排序，保证拓扑展开确定。
func uniqueSorted(devs []*compiledDevice) []*compiledDevice {
	seen := map[*compiledDevice]bool{}
	out := devs[:0]
	for _, d := range devs {
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}
