package err_code

import "fmt"

var (
	ErrInvalidDataType = NewError(10400, "无效的数据类型")
	ErrInternal        = NewError(10500, "内部错误")
)

type Error struct {
	code int
	msg  string
}

var codes = map[int]string{}

func NewError(code int, msg string) *Error {
	if _, ok := codes[code]; ok {
		panic(fmt.Sprintf("错误码 %d 已经存在，请更换一个", code))
	}
	codes[code] = msg
	return &Error{code: code, msg: msg}
}

func (e *Error) Code() int {
	return e.code
}

func (e *Error) Msg() string {
	return e.msg
}
