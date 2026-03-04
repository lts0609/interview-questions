package coding

import "fmt"

func main() {
	arr := []string{"a", "cat", "sit", "in", "the", "river", "in", "the", "river"}
	fmt.Println(question(arr, 3, 3))
}

func question(arr []string, nums, times int) bool {
	mp := map[string]int{}
	cnt := 0
	for _, v := range arr {
		mp[v]++
	}
	for _, v := range mp {
		if v == times {
			cnt++
		}
	}
	if cnt == nums {
		return true
	}
	return false
}
