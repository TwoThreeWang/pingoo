package utils

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Response 统一响应结构
type Response struct {
	Code int         `json:"code"`
	Msg  string      `json:"msg"`
	Data interface{} `json:"data,omitempty"`
}

// Success 返回成功响应
func Success(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, Response{
		Code: 0,
		Msg:  "success",
		Data: data,
	})
}

// Fail 返回失败响应
func Fail(c *gin.Context, msg string) {
	c.JSON(http.StatusBadRequest, Response{
		Code: 1,
		Msg:  msg,
	})
}

// FailWithCode 返回指定状态码的失败响应
func FailWithCode(c *gin.Context, code int, msg string) {
	c.JSON(code, Response{
		Code: 1,
		Msg:  msg,
	})
}

// PageResult 分页响应结构
type PageResult struct {
	List     interface{} `json:"list"`
	Total    int64       `json:"total"`
	Page     int         `json:"page"`
	PageSize int         `json:"page_size"`
}

// SuccessWithPage 返回分页成功响应
func SuccessWithPage(c *gin.Context, list interface{}, total int64, page, pageSize int) {
	c.JSON(http.StatusOK, Response{
		Code: 0,
		Msg:  "success",
		Data: PageResult{
			List:     list,
			Total:    total,
			Page:     page,
			PageSize: pageSize,
		},
	})
}

// ValidationError 返回验证错误响应
func ValidationError(c *gin.Context, msg string) {
	FailWithCode(c, http.StatusUnprocessableEntity, msg)
}

// NotFound 返回404错误响应
func NotFound(c *gin.Context, msg string) {
	if msg == "" {
		msg = "资源不存在"
	}
	FailWithCode(c, http.StatusNotFound, msg)
}

// ServerError 返回500错误响应
func ServerError(c *gin.Context, msg string) {
	if msg == "" {
		msg = "服务器内部错误"
	}
	FailWithCode(c, http.StatusInternalServerError, msg)
}