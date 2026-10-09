package platformhttp

const listResultTotalCap = 10000

func capListTotal(count int) (int, bool) {
	if count > listResultTotalCap {
		return listResultTotalCap, true
	}
	return count, false
}
