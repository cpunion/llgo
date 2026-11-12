package main
import("fmt"; "os"; "path/filepath"; "runtime"; "syscall"; "unsafe")
func main(){
 root,err:=filepath.EvalSymlinks(runtime.GOROOT());fmt.Printf("GOROOT=%q eval=%q err=%v\n",runtime.GOROOT(),root,err)
 proc:=syscall.NewLazyDLL("kernel32.dll").NewProc("GetFinalPathNameByHandleW")
 for _,name:=range []string{root,filepath.Join(root,"bin","go.exe"),os.TempDir()} {
 f,err:=os.Open(name);if err!=nil {panic(err)}
 buf:=make([]uint16,32768);n,_,err:=proc.Call(f.Fd(),uintptr(unsafe.Pointer(&buf[0])),uintptr(len(buf)),0)
 fmt.Printf("INPUT=%q FINAL=%q ERR=%v\n",name,syscall.UTF16ToString(buf[:n]),err);f.Close()
 }
}
