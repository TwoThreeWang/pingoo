package scheduler

import (
	"sync/atomic"
	"testing"
)

func TestSyncQueueDedupeAndRerun(t *testing.T) {
	s := &Scheduler{syncQ: make(chan int64, 8)}
	s.enqueueSync(1)
	s.enqueueSync(1) // 已经在排队：不重复放，记下“结束后再来一次”
	s.enqueueSync(2)
	if len(s.syncQ) != 2 {
		t.Fatalf("同一个网站不应该重复排队，队列里有 %d 个", len(s.syncQ))
	}
	if id := <-s.syncQ; id != 1 {
		t.Fatalf("先进先出，得到 %d", id)
	}
	s.finishSync(1) // 1 同步完：期间被要求过再同步，所以再排一次
	if len(s.syncQ) != 2 {
		t.Fatalf("同步期间又被要求同步的网站应该再排一次，队列里有 %d 个", len(s.syncQ))
	}
	<-s.syncQ // 2
	s.finishSync(2)
	<-s.syncQ // 1（第二次）
	s.finishSync(1)
	if len(s.syncQ) != 0 {
		t.Fatalf("没有新的请求就不应该再排，队列里有 %d 个", len(s.syncQ))
	}
	if _, busy := s.syncing.Load(int64(1)); busy {
		t.Fatal("同步结束后应该清掉标记")
	}

	// 队列满了：不阻塞，也不留下标记（否则这个网站永远排不进去）
	full := &Scheduler{syncQ: make(chan int64, 1)}
	full.enqueueSync(1)
	full.enqueueSync(2)
	if _, stuck := full.syncing.Load(int64(2)); stuck || len(full.syncQ) != 1 {
		t.Fatal("队列满时不应该留下标记")
	}
	var _ atomic.Bool
	// 调度器还没启动时调用不会出错
	(&Scheduler{}).enqueueSync(1)
}
