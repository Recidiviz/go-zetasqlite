package internal

import (
	"fmt"
	"unsafe"
)

func main() {
	fmt.Println(unsafe.Sizeof(IntValue{Value: int64(5)}))
}
