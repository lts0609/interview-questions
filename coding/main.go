package main

import (
	"fmt"
	"sync"
)

// 如果要打印给定的内容 可以加一个channel和goroutine 按顺序写入
func main() {
	var wg sync.WaitGroup
	done := make(chan struct{})

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fmt.Println(i)
			done <- struct{}{}
		}()
		<-done
	}

	wg.Wait()
}
