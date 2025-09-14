package controllers

import (
	"strings"
	"time"

	"pingoo/config"
	"pingoo/middleware"
	"pingoo/models"
	"pingoo/utils"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// AuthController 认证控制器
type AuthController struct {
	db     *gorm.DB
	config *config.Config
}

// NewAuthController 创建认证控制器
func NewAuthController(db *gorm.DB, config *config.Config) *AuthController {
	return &AuthController{
		db:     db,
		config: config,
	}
}

// Register 用户注册
func (ac *AuthController) Register(c *gin.Context) {
	var input models.UserCreate
	if err := c.ShouldBindJSON(&input); err != nil {
		utils.ValidationError(c, err.Error())
		return
	}

	// 从 email 中提取用户名
	username := strings.Split(input.Email, "@")[0]

	// 检查用户名是否已存在
	var existingUser models.User
	if err := ac.db.Where("username = ? OR email = ?", username, input.Email).First(&existingUser).Error; err == nil {
		utils.Fail(c, "用户名或邮箱已存在")
		return
	}

	// 加密密码
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		utils.ServerError(c, "密码加密失败")
		return
	}

	// 创建用户
	user := models.User{
		Username: username,
		Email:    input.Email,
		Password: string(hashedPassword),
		Role:     "user",
	}

	if err = ac.db.Create(&user).Error; err != nil {
		utils.ServerError(c, "用户创建失败")
		return
	}

	// 生成JWT token
	token, err := middleware.GenerateToken(ac.config, &user)
	if err != nil {
		utils.ServerError(c, "令牌生成失败")
		return
	}

	// 生成刷新token
	refreshToken, err := middleware.GenerateRefreshToken(ac.config, &user)
	if err != nil {
		utils.ServerError(c, "刷新令牌生成失败")
		return
	}

	// 返回用户信息（不包含密码）
	userResponse := models.UserResponse{
		ID:       uint64(user.ID),
		Username: user.Username,
		Email:    user.Email,
		Role:     user.Role,
	}
	utils.Success(c, models.TokenResponse{
		Token:        token,
		RefreshToken: refreshToken,
		User:         userResponse,
		ExpiresIn:    int64(ac.config.JWT.ExpireHours * 3600),
	})
}

// Login 用户登录
func (ac *AuthController) Login(c *gin.Context) {
	var input models.UserLogin
	if err := c.ShouldBindJSON(&input); err != nil {
		utils.ValidationError(c, err.Error())
		return
	}

	// 查找用户
	var user models.User
	if err := ac.db.Where("email = ?", input.Email).First(&user).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			utils.Fail(c, "邮箱或密码错误")
		} else {
			utils.ServerError(c, "数据库查询错误")
		}
		return
	}

	// 验证密码
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(input.Password)); err != nil {
		utils.Fail(c, "邮箱或密码错误")
		return
	}

	// 更新最后登录时间
	now := time.Now()
	user.LastLogin = &now
	if err := ac.db.Save(&user).Error; err != nil {
		utils.ServerError(c, "更新最后登录时间失败")
		return
	}

	// 生成JWT token
	token, err := middleware.GenerateToken(ac.config, &user)
	if err != nil {
		utils.ServerError(c, "令牌生成失败")
		return
	}

	// 生成刷新token
	refreshToken, err := middleware.GenerateRefreshToken(ac.config, &user)
	if err != nil {
		utils.ServerError(c, "生成刷新令牌失败")
		return
	}

	// 返回用户信息
	userResponse := models.UserResponse{
		ID:       uint64(user.ID),
		Username: user.Username,
		Email:    user.Email,
		Role:     user.Role,
	}

	expireHours := ac.config.JWT.ExpireHours
	c.SetCookie("token", token, expireHours*3600, "/", "", false, true)
	c.SetCookie("refresh_token", refreshToken, expireHours*3600, "/", "", false, true)

	utils.Success(c, models.TokenResponse{
		Token:        token,
		RefreshToken: refreshToken,
		User:         userResponse,
		ExpiresIn:    int64(ac.config.JWT.ExpireHours * 3600),
	})
}

// RefreshToken 刷新token
func (ac *AuthController) RefreshToken(c *gin.Context) {
	var input struct {
		RefreshToken string `json:"refresh_token" binding:"required"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		utils.ValidationError(c, err.Error())
		return
	}

	// 验证刷新token
	claims := &middleware.JWTClaims{}
	token, err := jwt.ParseWithClaims(input.RefreshToken, claims, func(token *jwt.Token) (interface{}, error) {
		return []byte(ac.config.JWT.SecretKey), nil
	})

	if err != nil || !token.Valid {
		utils.Fail(c, "无效的刷新令牌")
		return
	}

	// 查找用户
	var user models.User
	if err = ac.db.First(&user, claims.UserID).Error; err != nil {
		utils.Fail(c, "用户不存在")
		return
	}

	// 生成新的JWT token
	newToken, err := middleware.GenerateToken(ac.config, &user)
	if err != nil {
		utils.ServerError(c, "新令牌生成失败")
		return
	}

	// 生成新的刷新token
	newRefreshToken, err := middleware.GenerateRefreshToken(ac.config, &user)
	if err != nil {
		utils.ServerError(c, "生成新的刷新令牌失败")
		return
	}

	userResponse := models.UserResponse{
		ID:       uint64(user.ID),
		Username: user.Username,
		Email:    user.Email,
		Role:     user.Role,
	}

	utils.Success(c, models.TokenResponse{
		Token:        newToken,
		RefreshToken: newRefreshToken,
		User:         userResponse,
		ExpiresIn:    int64(ac.config.JWT.ExpireHours * 3600),
	})
}

// Me 获取当前用户信息
func (ac *AuthController) Me(c *gin.Context) {
	userID := middleware.GetCurrentUserID(c)

	var user models.User
	if err := ac.db.First(&user, userID).Error; err != nil {
		utils.Fail(c, "用户不存在")
		return
	}

	userResponse := models.UserResponse{
		ID:       uint64(user.ID),
		Username: user.Username,
		Email:    user.Email,
		Role:     user.Role,
	}

	utils.Success(c, userResponse)
}

// UpdateProfile 更新用户资料
func (ac *AuthController) UpdateProfile(c *gin.Context) {
	userID := middleware.GetCurrentUserID(c)

	var input models.UserUpdate
	if err := c.ShouldBindJSON(&input); err != nil {
		utils.ValidationError(c, err.Error())
		return
	}

	var user models.User
	if err := ac.db.First(&user, userID).Error; err != nil {
		utils.Fail(c, "用户不存在")
		return
	}

	// 更新用户信息
	if input.Username != "" {
		user.Username = input.Username
	}
	if input.Email != "" {
		user.Email = input.Email
	}

	if err := ac.db.Save(&user).Error; err != nil {
		utils.ServerError(c, "更新用户资料失败")
		return
	}

	userResponse := models.UserResponse{
		ID:       uint64(user.ID),
		Username: user.Username,
		Email:    user.Email,
		Role:     user.Role,
	}

	utils.Success(c, userResponse)
}

// ChangePassword 修改密码
func (ac *AuthController) ChangePassword(c *gin.Context) {
	userID := middleware.GetCurrentUserID(c)

	var input models.ChangePassword
	if err := c.ShouldBindJSON(&input); err != nil {
		utils.ValidationError(c, err.Error())
		return
	}

	var user models.User
	if err := ac.db.First(&user, userID).Error; err != nil {
		utils.Fail(c, "用户不存在")
		return
	}

	// 验证旧密码
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(input.OldPassword)); err != nil {
		utils.Fail(c, "当前密码错误")
		return
	}

	// 加密新密码
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(input.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		utils.ServerError(c, "密码加密失败")
		return
	}

	// 更新密码
	user.Password = string(hashedPassword)
	if err := ac.db.Save(&user).Error; err != nil {
		utils.ServerError(c, "密码修改失败")
		return
	}

	utils.Success(c, "密码修改成功")
}
