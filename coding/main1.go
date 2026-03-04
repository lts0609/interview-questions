package coding

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

// task1: 等待所有任务完成 用 sleep 太 low 了，请换一个优雅的实现
// task2: 增加并发限制, 最多同时执行100个 fetchSomething 任务。任务生成不会阻塞。
// task3: 增加一个计数器，统计一共执行了多少次 fetchSomething 函数
// task4: 结果写在 results 中

var counterTask int32 = 0

var results = sync.Map{}

func fetchSomething(url string) {
	time.Sleep(1 * time.Second)
	fmt.Printf("fetch %s\n", url)
	results.Store(url, url)
}

func main() {
	t := time.Now()
	realTaskCount := rand.Intn(10) + 500
	wg := sync.WaitGroup{}
	ch := make(chan struct{}, 100)
	wg.Add(realTaskCount)
	for i := 0; i < realTaskCount; i++ {
		ch <- struct{}{}
		go func(ch chan struct{}, wg *sync.WaitGroup) {
			atomic.AddInt32(&counterTask, 1)
			fetchSomething(fmt.Sprintf("https://example.com/%d", i))
			wg.Done()
			<-ch
		}(ch, &wg)
	}
	wg.Wait()

	fmt.Printf("time: %v\n", time.Since(t))
	fmt.Printf("counterTask: %d, realTaskCount: %d\n", counterTask, realTaskCount)
}
