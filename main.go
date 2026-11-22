// fiche-go: pastebin de linha de comando (texto e binarios) compativel com o fiche em C.
//
//	echo teste | nc bin.tux.tools 9999        -> https://bin.tux.tools/abcd/
//	nc bin.tux.tools 9999 < arquivo.tar.gz    -> https://bin.tux.tools/efgh/  (download)
//
// Os dados vao direto para o disco (memoria constante por conexao). Texto UTF-8 e gravado
// como index.txt e qualquer outra coisa como index.bin; o servidor web serve o diretorio.
package main

import (
	"bytes"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

const slugChars = "abcdefghijklmnopqrstuvwxyz0123456789"

type config struct {
	domain    string
	outDir    string
	listen    string
	port      int
	slugLen   int
	https     bool
	maxBytes  int64
	logFile   string
	maxConns  int
	maxPerIP  int
	idle      time.Duration
	timeout   time.Duration
	minFreeMB uint64
}

type server struct {
	cfg   config
	sem   chan struct{}
	mu    sync.Mutex
	perIP map[string]int
	wg    sync.WaitGroup
}

func main() {
	var c config
	flag.StringVar(&c.domain, "d", "localhost", "dominio usado na URL devolvida")
	flag.StringVar(&c.outDir, "o", "./code", "diretorio onde os pastes sao gravados")
	flag.StringVar(&c.listen, "L", "0.0.0.0", "endereco de escuta")
	flag.IntVar(&c.port, "p", 9999, "porta")
	flag.IntVar(&c.slugLen, "s", 4, "tamanho do slug")
	flag.BoolVar(&c.https, "S", false, "devolver URL https://")
	flag.Int64Var(&c.maxBytes, "B", 10<<20, "tamanho maximo de um paste, em bytes")
	flag.StringVar(&c.logFile, "l", "", "arquivo de log (alem da saida padrao)")
	flag.IntVar(&c.maxConns, "max-conns", 64, "conexoes simultaneas no total")
	flag.IntVar(&c.maxPerIP, "max-per-ip", 4, "conexoes simultaneas por IP")
	flag.DurationVar(&c.idle, "idle", 5*time.Second, "fim do envio apos este tempo sem dados")
	flag.DurationVar(&c.timeout, "timeout", 3*time.Minute, "tempo maximo de uma conexao")
	flag.Uint64Var(&c.minFreeMB, "min-free", 2048, "recusar envios se o disco tiver menos MB livres que isto")
	flag.Parse()

	if c.slugLen < 2 || c.slugLen > 32 || c.maxBytes < 1 || c.maxConns < 1 || c.maxPerIP < 1 {
		log.Fatal("parametros invalidos")
	}
	if fi, err := os.Stat(c.outDir); err != nil || !fi.IsDir() {
		log.Fatalf("diretorio de saida invalido: %s", c.outDir)
	}
	if c.logFile != "" {
		f, err := os.OpenFile(c.logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
		if err != nil {
			log.Fatalf("log: %v", err)
		}
		defer f.Close()
		log.SetOutput(io.MultiWriter(os.Stdout, f))
	}

	ln, err := net.Listen("tcp", net.JoinHostPort(c.listen, fmt.Sprint(c.port)))
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	s := &server{cfg: c, sem: make(chan struct{}, c.maxConns), perIP: map[string]int{}}
	log.Printf("fiche-go escutando em %s (dominio %s, max %d bytes, saida %s)", ln.Addr(), c.domain, c.maxBytes, c.outDir)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		log.Print("encerrando: aguardando conexoes em andamento")
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				break
			}
			log.Printf("accept: %v", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		s.wg.Add(1)
		go s.handle(conn)
	}
	s.wg.Wait()
}

func (s *server) handle(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()
	ip, _, _ := net.SplitHostPort(conn.RemoteAddr().String())

	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	default:
		reply(conn, "ERROR: server busy, try again later\n")
		log.Printf("%s recusado: limite global de conexoes", ip)
		return
	}
	if !s.acquireIP(ip) {
		reply(conn, "ERROR: too many simultaneous connections from your IP\n")
		log.Printf("%s recusado: limite por IP", ip)
		return
	}
	defer s.releaseIP(ip)

	if s.diskLow() {
		reply(conn, "ERROR: server out of space, try again later\n")
		log.Printf("%s recusado: pouco espaco em disco", ip)
		return
	}

	slug, dir, err := s.newSlugDir()
	if err != nil {
		reply(conn, "ERROR: internal error\n")
		log.Printf("%s slug: %v", ip, err)
		return
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(dir)
		}
	}()

	n, binary, err := s.receive(conn, dir)
	switch {
	case errors.Is(err, errTooLarge):
		reply(conn, fmt.Sprintf("ERROR: paste too large (max %d bytes)\n", s.cfg.maxBytes))
		log.Printf("%s recusado: maior que %d bytes", ip, s.cfg.maxBytes)
		return
	case err != nil:
		log.Printf("%s erro na recepcao: %v", ip, err)
		return
	case n == 0:
		reply(conn, "ERROR: empty paste\n")
		return
	}
	ok = true

	scheme := "http://"
	if s.cfg.https {
		scheme = "https://"
	}
	kind := "texto"
	if binary {
		kind = "binario"
	}
	reply(conn, fmt.Sprintf("%s%s/%s/\n", scheme, s.cfg.domain, slug))
	log.Printf("%s -- %s -- %d bytes (%s)", slug, ip, n, kind)
}

var errTooLarge = errors.New("paste muito grande")

// receive grava a conexao em dir/.upload e renomeia para index.txt ou index.bin.
// O envio termina quando o cliente fecha a escrita (EOF) ou fica s.cfg.idle sem mandar dados
// (o nc tradicional nao fecha a escrita; o fiche em C tambem usava timeout de 5s).
func (s *server) receive(conn net.Conn, dir string) (int64, bool, error) {
	tmp := filepath.Join(dir, ".upload")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, false, err
	}
	defer f.Close()

	buf := make([]byte, 32<<10)
	var total int64
	var sniff []byte
	deadline := time.Now().Add(s.cfg.timeout)
	for {
		d := time.Now().Add(s.cfg.idle)
		if d.After(deadline) {
			d = deadline
		}
		conn.SetReadDeadline(d)
		m, rerr := conn.Read(buf)
		if m > 0 {
			total += int64(m)
			if total > s.cfg.maxBytes {
				return total, false, errTooLarge
			}
			if len(sniff) < 8192 {
				sniff = append(sniff, buf[:min(m, 8192-len(sniff))]...)
			}
			if _, werr := f.Write(buf[:m]); werr != nil {
				return total, false, werr
			}
		}
		if rerr != nil {
			var ne net.Error
			isTimeout := errors.As(rerr, &ne) && ne.Timeout()
			if errors.Is(rerr, io.EOF) || (isTimeout && time.Now().Before(deadline)) {
				break // fim normal: EOF ou inatividade
			}
			if isTimeout && total > 0 {
				break // tempo total esgotado: guarda o que chegou
			}
			return total, false, rerr
		}
	}
	if err := f.Close(); err != nil {
		return total, false, err
	}
	if total == 0 {
		return 0, false, nil
	}
	binary := isBinary(sniff)
	name := "index.txt"
	if binary {
		name = "index.bin"
	}
	return total, binary, os.Rename(tmp, filepath.Join(dir, name))
}

// isBinary: tem byte nulo ou nao e UTF-8 valido (ignorando um caractere cortado no fim da amostra).
func isBinary(b []byte) bool {
	if bytes.IndexByte(b, 0) >= 0 {
		return true
	}
	for i := 0; i < utf8.UTFMax && len(b) > 0 && !utf8.Valid(b); i++ {
		b = b[:len(b)-1]
	}
	return !utf8.Valid(b)
}

func (s *server) newSlugDir() (string, string, error) {
	n := s.cfg.slugLen
	for attempt := 0; attempt < 200; attempt++ {
		if attempt > 0 && attempt%20 == 0 {
			n++ // espaco de slugs ficando cheio: aumenta o tamanho
		}
		slug, err := randomSlug(n)
		if err != nil {
			return "", "", err
		}
		dir := filepath.Join(s.cfg.outDir, slug)
		err = os.Mkdir(dir, 0o755)
		if err == nil {
			return slug, dir, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", "", err
		}
	}
	return "", "", errors.New("nao foi possivel gerar um slug livre")
}

func randomSlug(n int) (string, error) {
	b := make([]byte, n)
	max := big.NewInt(int64(len(slugChars)))
	for i := range b {
		r, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = slugChars[r.Int64()]
	}
	return string(b), nil
}

func (s *server) acquireIP(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.perIP[ip] >= s.cfg.maxPerIP {
		return false
	}
	s.perIP[ip]++
	return true
}

func (s *server) releaseIP(ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.perIP[ip]--; s.perIP[ip] <= 0 {
		delete(s.perIP, ip)
	}
}

func (s *server) diskLow() bool {
	var st syscall.Statfs_t
	if err := syscall.Statfs(s.cfg.outDir, &st); err != nil {
		return false
	}
	return st.Bavail*uint64(st.Bsize)/(1<<20) < s.cfg.minFreeMB
}

func reply(conn net.Conn, msg string) {
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	io.WriteString(conn, msg)
}
