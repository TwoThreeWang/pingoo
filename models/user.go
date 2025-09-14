package models

import (
	"time"

	"gorm.io/gorm"
)

// User 用户模型
type User struct {
	gorm.Model            // 自动添加 ID、CreatedAt、UpdatedAt、DeletedAt 字段
	Username   string     `gorm:"type:varchar(50);uniqueIndex;not null" json:"username"`
	Email      string     `gorm:"type:varchar(100);uniqueIndex;not null" json:"email"`
	Password   string     `gorm:"type:varchar(255);not null" json:"-"`         // 不返回密码
	Role       string     `gorm:"type:varchar(20);default:'user'" json:"role"` // user, admin
	LastLogin  *time.Time `gorm:"index" json:"last_login,omitempty"`
}

// TableName 设置表名
func (User) TableName() string {
	return "users"
}

// UserCreate 用户注册结构体
type UserCreate struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6,max=32"`
}

// UserLogin 用户登录结构体
type UserLogin struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

// UserUpdate 用户更新结构体
type UserUpdate struct {
	Username string `json:"username" binding:"required,min=3,max=50"`
	Email    string `json:"email" binding:"required,email"`
}

// UserResponse 用户响应结构体（不包含敏感信息）
type UserResponse struct {
	ID       uint64 `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
	Role     string `json:"role"`
}

// ChangePassword 修改密码结构体
type ChangePassword struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=6,max=32"`
}

// TokenResponse Token响应结构体
type TokenResponse struct {
	Token        string       `json:"token"`
	RefreshToken string       `json:"refresh_token,omitempty"`
	User         UserResponse `json:"user"`
	ExpiresIn    int64        `json:"expires_in"`
}
