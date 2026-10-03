package main 
import ( 
    "fmt" 
    "golang.org/x/crypto/bcrypt" 
) 
func main() { 
    hash, _ := bcrypt.GenerateFromPassword([]byte("AgentPass123"), bcrypt.DefaultCost) 
    fmt.Println(string(hash)) 
} 
