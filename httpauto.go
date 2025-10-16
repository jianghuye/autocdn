package main

import (
    "io"
    "log"
    "net"
    "net/http"
    "os"
    "time"
    "math/rand"
    "sort"
    "sync"
    "strings"
    "golang.org/x/net/publicsuffix"
)

type autoIP struct {
    rip    string //IP
    Timems int //延迟 ms
}

var iptest[] autoIP
var delockd sync.RWMutex
var autoC byte
var Inits byte
var cache sync.Map
var debugOn bool
var forceCdn sync.Map

func main() {
    argc:=len(os.Args)
    if argc <2 {
        log.Println("HELP: server 127.0.0.1:8081 ips.txt debug=on/off(Optional,default=off)")
        return
    }
    if argc ==4 {
        if os.Args[3]=="debug=on"{
            debugOn=true
            log.Println("Debug: debug on")
        }
    }
    ipaddr:=os.Args[1]
    server := &http.Server{
        Addr: ipaddr,
        Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            if r.Method == http.MethodConnect {
                handleConnect(w, r)
            } else {
                handleHTTP(w, r)
            }
        }),
    }
    rd, err := os.ReadFile(os.Args[2])
    if err != nil {
        log.Println("Err: reading ips file:", err)
        return
    }
    var testip[] string
    lines := strings.Split(string(rd), "\n")
    for _, line := range lines {
        if line != "" {
        rt := strings.ReplaceAll(line, "\r", "")
        if !strings.Contains(rt, ":") {rt+=":443";}
        testip = append(testip, rt)
        log.Println("INFO: load ip:", rt)
        }
    }
    iptest=make([]autoIP,len(testip))
    for i, ip := range testip {
        iptest[i].rip=ip
        iptest[i].Timems=2000
    }
    testip=nil
    rand.Seed(time.Now().UnixNano())
    go auto排序();
    rd, err = os.ReadFile("forcecdn.txt")
    if err == nil {
    log.Println("INFO: enable force cdn")
    lines := strings.Split(string(rd), "\n")
    for _, line := range lines {
        if line != "" {
        rt := strings.ReplaceAll(line, "\r", "")
        forceCdn.Store(rt,true)
        log.Println("INFO: load force domain:", rt)
        }
    }
    }
    log.Println("INFO: server listening on:",ipaddr)
    if err := server.ListenAndServe(); err != nil {
        log.Fatal(err)
    }
}
func handleConnect(w http.ResponseWriter, r *http.Request) {
    hijacker, ok := w.(http.Hijacker)
    if !ok {
        http.Error(w, "connect not supported", http.StatusInternalServerError)
        return
    }
    clientConn, _, err := hijacker.Hijack()
    if err != nil {
        if debugOn {
        log.Println("Debug: error:", err)
        }
        return
    }
    domain, _, err := net.SplitHostPort(r.Host)
    if err != nil {
        domain = r.Host
    }
    _,ok=forceCdn.Load(domain)
    var rconn net.Conn
    if nslookcdn(r.Host) || ok {
        if debugOn {
        log.Println("Debug: find IP:",r.Host)
        }
        n,rip:=autoGetip()
        start := time.Now()
        rconn, err = net.Dial("tcp", rip)
        timems := int(time.Since(start).Milliseconds())
        if err!=nil {
            deleteIP(n)
            log.Println("INFO: auto delete IP with:",err,rip)
        } else {
            if timems > 800 {
                deleteIP(n)
                log.Println("INFO: auto delete IP with: time ms >800 ",rip)
            } else {
                iptest[n].Timems=timems
            }
        }
    } else {
        rconn, err = net.Dial("tcp", r.Host)
    }
    if err != nil {
        if debugOn {
        log.Println("Debug: Dial:", err)
        }
        clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
        clientConn.Close()
        return
    }
    clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
    ends := make(chan bool, 2)
    go func() {
        io.Copy(rconn, clientConn)
        ends <- true
    }()
    go func() {
        io.Copy(clientConn, rconn)
        ends <- true
    }()
    <-ends
    clientConn.Close()
    rconn.Close()
    <-ends
}
func handleHTTP(w http.ResponseWriter, r *http.Request) {
    if r.URL.Scheme == "" {
        r.URL.Scheme = "http"
    }
    if r.URL.Host == "" {
        r.URL.Host = r.Host
    }
    resp, err := http.DefaultTransport.RoundTrip(r)
    if err != nil {
        http.Error(w, err.Error(), http.StatusServiceUnavailable)
        return
    }
    defer resp.Body.Close()
    for k, vv := range resp.Header {
        for _, v := range vv {
            w.Header().Add(k, v)
        }
    }
    w.WriteHeader(resp.StatusCode)
    io.Copy(w, resp.Body)
}
func deleteIP(n int){
    if n >= 0 && n < len(iptest) {
        delockd.Lock()
        iptest = append(iptest[:n], iptest[n+1:]...)
        delockd.Unlock()
    }
}
func autoGetip() (int,string) {
    delockd.RLock()
    defer delockd.RUnlock()
    n:=0
    if Inits < 250 {
        Inits++
        n = rand.Intn(len(iptest))
    } else {
    if (autoC &7) == 0 {
        n = rand.Intn(len(iptest))
    } else {
        n = rand.Intn(len(iptest)/2)
    }
    autoC++
    }
    return n,iptest[n].rip
}

func auto排序(){
for{
    time.Sleep(time.Second * 60)
    if Inits > 240 {
    delockd.Lock()
    sort.Slice(iptest, func(i, j int) bool {
    return iptest[i].Timems < iptest[j].Timems
    })
    delockd.Unlock()
    }
}}

func nslookcdn(addr string) bool {
    domain, _, err := net.SplitHostPort(addr)
    if err != nil {
        domain = addr
    }
    _,ok:=cache.Load(domain)
    if ok {
        return true
    }
    dm, err := publicsuffix.EffectiveTLDPlusOne(domain)
    if err != nil {
        if debugOn {
        log.Println("Debug: Error:", err)
        }
        return false
    }
    nsRecords, err := net.LookupNS(dm)
    if err != nil {
        if debugOn {
        log.Println("Debug: Error:", err)
        }
        return false
    }
    onCdn:=false
    for _, ns := range nsRecords {
        if strings.HasSuffix(ns.Host, ".cloudflare.com.") {
            onCdn=true
        }
    }
    if onCdn {
    nsRecords, err = net.LookupNS(domain)
    if err != nil {
        cache.Store(domain,true)
		time.AfterFunc(time.Second*600,func(){
		cache.Delete(domain)
			})
		return true
    }
    for _, ns := range nsRecords {
        if strings.HasSuffix(ns.Host, ".cloudflare.com.") {
            cache.Store(domain,true)
            time.AfterFunc(time.Second*600,func(){
            cache.Delete(domain)
            })
            return true
        }
    }
    }
    return false
}