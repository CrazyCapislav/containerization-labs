// api — подопытный сервис для лабы 1.
//
// Три эндпоинта, каждый бьёт в свой механизм ядра:
//   GET /health      — жив ли процесс
//   GET /eat?mb=N    — съесть N МиБ памяти и держать  (лимит памяти, OOM)
//   GET /burn        — занять одно ядро в вечном цикле (лимит CPU, throttling)
//
// Без внешних зависимостей: собирается статикой и кладётся в scratch.
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

const pageSize = 4096

var (
	mu      sync.Mutex
	ballast [][]byte // держим ссылки, иначе GC заберёт память обратно
	eatenMB int64
	burners int64
)

func health(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "ok")
}

// eat выделяет N МиБ и удерживает их до конца жизни процесса.
func eat(w http.ResponseWriter, r *http.Request) {
	mb, err := strconv.Atoi(r.URL.Query().Get("mb"))
	if err != nil || mb <= 0 {
		http.Error(w, "нужен параметр mb > 0, например /eat?mb=100\n", http.StatusBadRequest)
		return
	}

	block := make([]byte, mb<<20)
	// Само по себе выделение виртуального адресного пространства ничего не стоит:
	// страница становится резидентной (и попадает в счётчик cgroup) только когда
	// в неё записали. Поэтому касаемся по байту на страницу.
	for i := 0; i < len(block); i += pageSize {
		block[i] = 1
	}

	mu.Lock()
	ballast = append(ballast, block)
	mu.Unlock()

	total := atomic.AddInt64(&eatenMB, int64(mb))

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	fmt.Fprintf(w, "выделено %d МиБ, всего удерживается %d МиБ (heap %d МиБ)\n",
		mb, total, ms.HeapAlloc>>20)
}

// burn занимает одно ядро бесконечным циклом.
func burn(w http.ResponseWriter, r *http.Request) {
	n := atomic.AddInt64(&burners, 1)

	go func() {
		// Привязка к треду ОС: нагрузка ложится на одного носителя,
		// а не размазывается планировщиком Go по всем P.
		runtime.LockOSThread()
		var x uint64
		for {
			x++
			_ = x % 7
		}
	}()

	fmt.Fprintf(w, "запущен жигатель #%d, ядер в системе: %d\n", n, runtime.NumCPU())
}

func index(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, "api\n\n  GET /health\n  GET /eat?mb=N\n  GET /burn\n")
}

func main() {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", index)
	mux.HandleFunc("/health", health)
	mux.HandleFunc("/eat", eat)
	mux.HandleFunc("/burn", burn)

	log.Printf("api слушает %s, pid %d", addr, os.Getpid())
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
