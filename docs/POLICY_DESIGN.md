# ClawSSH Policy Engine 设计说明

## 1. 策略结构

### PolicyResult (rules.go)

```go
type PolicyResult struct {
    Allowed     bool   // false = 拒绝执行
    NeedConfirm bool   // true = 需用户输入 yes 确认
    Reason      string // 人类可读的原因，用于审计和提示
}
```

### 风险等级 (RiskLevel)

- **low**: 直接放行
- **medium**: 在 Production 环境下需确认；在 Staging/Development 直接放行
- **high**: 所有环境均需用户确认

### 配置文件 (configs/policies.yaml)

```yaml
actions:
  check_cpu: low
  service_control: high
  read_file: medium
  # ...

env_overrides:
  production:
    service_control: high
    firewall_status: high
  staging:
    service_control: medium   # staging 下降级为 medium
```

通过环境变量 `CLAWSSH_POLICIES` 指定自定义策略文件路径；未设置则使用内置默认规则。

## 2. 接口与判定逻辑

### PolicyEngine.Evaluate 接口

```go
func (p *PolicyEngine) Evaluate(ctx *policy.Context, task *dsl.Task) *PolicyResult
```

### Context (环境感知)

```go
type Context struct {
    Environment string // "production", "staging", "development"
    Username    string
    RemoteAddr  string
}
```

环境由 `CLAWSSH_ENV` 或 `server.Config.Environment` 提供，默认 `development`。

### 判定流程

1. **Allowlist**: Action 和 Target 必须在白名单内
2. **Blacklist**: 检查 `Args` 和 `Parameters` 中是否包含危险字符（`;`, `&&`, `|`, `` ` ``, `$(`, `${` 等）
3. **风险等级**: 根据 `actions` 和 `env_overrides` 获取当前环境的 risk
4. **决策**:
   - high → `AllowWithConfirm`
   - medium + production → `AllowWithConfirm`
   - medium + 其他 → `Allow`
   - low → `Allow`

## 3. 执行流集成

```
用户输入 → IntentEngine.Parse → PolicyEngine.Evaluate
                                    ↓
                            ┌───────┴───────┐
                            │ !Allowed      │ → 拒绝，返回 Reason
                            └───────┬───────┘
                                    │ Allowed
                                    ↓
                            ┌───────┴───────┐
                            │ NeedConfirm?  │ → 向 Session 写警告，readLine 等待
                            │ 输入 yes?     │   → 否: 返回「操作已取消」
                            └───────┬───────┘   → 是: 继续
                                    ↓
                            Audit.Log(Entry)
                                    ↓
                            tools.Execute(task)
```

## 4. 审计 (internal/audit)

所有操作（含低风险）均记录：

- User, Input, Action, Target
- DSL (JSON 序列化 task)
- Timestamp, RemoteAddr, SessionID

第一阶段输出到 slog；后续可扩展为文件或外部存储。

## 5. 使用示例

### 启动时指定环境

```bash
CLAWSSH_ENV=production CLAWSSH_POLICIES=./configs/policies.yaml clawssh
```

### 高风险操作交互

```
claw> restart nginx

[!] 操作 "service_control" 为高风险，请确认后执行
确认执行？输入 yes 继续，其他任意输入取消: yes
```

### 用户取消

```
确认执行？输入 yes 继续，其他任意输入取消: no
操作已取消。
```
