// @ts-nocheck
import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { AlertTriangle, Copy, ShieldCheck, Ticket, Trash2, User } from 'lucide-react'
import { api } from '@/lib/api'
import { handleServerError } from '@/lib/handle-server-error'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Badge } from '@/components/ui/badge'
import { Switch } from '@/components/ui/switch'
import { Label } from '@/components/ui/label'

// 邀请码管理弹窗（本仓库对上游的增量功能）。
// 邀请码一次性：A 开头 = 管理员，B 开头 = 普通用户；用过即失效。
// 管理员码风险高，生成后显著提示"请勿外泄"。
export function InviteCodeDialog({ open, onOpenChange }) {
  const queryClient = useQueryClient()
  const [lastCreated, setLastCreated] = useState<{ code: string; role: string } | null>(null)

  const codesQuery = useQuery({
    queryKey: ['invite-codes'],
    enabled: open,
    queryFn: async () => {
      const response = await api.get('/api/admin/invite-codes')
      return response.data as { invite_codes: any[] }
    },
  })

  const settingsQuery = useQuery({
    queryKey: ['register-settings'],
    enabled: open,
    queryFn: async () => {
      const response = await api.get('/api/admin/register-settings')
      return response.data as { enabled: boolean }
    },
  })

  const createMutation = useMutation({
    mutationFn: async (role: string) => {
      const response = await api.post('/api/admin/invite-codes', { role })
      return response.data as { invite_code: { code: string; role: string } }
    },
    onSuccess: (data) => {
      setLastCreated({ code: data.invite_code.code, role: data.invite_code.role })
      toast.success('邀请码已生成')
      queryClient.invalidateQueries({ queryKey: ['invite-codes'] })
    },
    onError: (error) => handleServerError(error),
  })

  const deleteMutation = useMutation({
    mutationFn: async (code: string) => {
      await api.delete(`/api/admin/invite-codes/${code}`)
    },
    onSuccess: () => {
      toast.success('邀请码已吊销')
      queryClient.invalidateQueries({ queryKey: ['invite-codes'] })
    },
    onError: (error) => handleServerError(error),
  })

  const toggleMutation = useMutation({
    mutationFn: async (enabled: boolean) => {
      const response = await api.put('/api/admin/register-settings', { enabled })
      return response.data as { enabled: boolean }
    },
    onSuccess: (data) => {
      toast.success(data.enabled ? '已开放注册' : '已关闭注册')
      queryClient.invalidateQueries({ queryKey: ['register-settings'] })
      queryClient.invalidateQueries({ queryKey: ['register-status'] })
    },
    onError: (error) => handleServerError(error),
  })

  const copyCode = async (code: string) => {
    try {
      await navigator.clipboard.writeText(code)
      toast.success(`已复制 ${code}`)
    } catch {
      toast.error('复制失败，请手动记录')
    }
  }

  const codes = codesQuery.data?.invite_codes ?? []
  const registerEnabled = settingsQuery.data?.enabled ?? false

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className='max-h-[85vh] overflow-y-auto sm:max-w-lg'>
        <DialogHeader>
          <DialogTitle className='flex items-center gap-2'>
            <Ticket className='h-4 w-4' />
            邀请码
          </DialogTitle>
          <DialogDescription>
            邀请码仅可使用一次。A 开头可注册管理员，B 开头注册普通用户。
          </DialogDescription>
        </DialogHeader>

        {/* 注册总开关 */}
        <div className='flex items-center justify-between rounded-lg border p-3'>
          <div className='space-y-0.5'>
            <Label htmlFor='register-enabled' className='text-sm font-medium'>
              开放自助注册
            </Label>
            <p className='text-xs text-muted-foreground'>
              关闭后 /register 页面会提示未开放，邀请码也失效
            </p>
          </div>
          <Switch
            id='register-enabled'
            checked={registerEnabled}
            disabled={toggleMutation.isPending || settingsQuery.isLoading}
            onCheckedChange={(checked) => toggleMutation.mutate(checked)}
          />
        </div>

        {/* 生成按钮 */}
        <div className='flex flex-wrap gap-2'>
          <Button
            size='sm'
            variant='outline'
            disabled={createMutation.isPending}
            onClick={() => createMutation.mutate('user')}
          >
            <User className='mr-2 h-4 w-4' />
            生成普通用户码
          </Button>
          <Button
            size='sm'
            variant='outline'
            disabled={createMutation.isPending}
            onClick={() => createMutation.mutate('admin')}
          >
            <ShieldCheck className='mr-2 h-4 w-4' />
            生成管理员码
          </Button>
        </div>

        {/* 刚生成的码：管理员码带强警告 */}
        {lastCreated && (
          <div
            className={
              lastCreated.role === 'admin'
                ? 'space-y-2 rounded-lg border border-amber-500/50 bg-amber-50 p-3 dark:bg-amber-950/20'
                : 'space-y-2 rounded-lg border bg-muted/40 p-3'
            }
          >
            <div className='flex items-center justify-between'>
              <span className='font-mono text-xl font-semibold tracking-widest'>
                {lastCreated.code}
              </span>
              <div className='flex items-center gap-2'>
                <Badge variant={lastCreated.role === 'admin' ? 'destructive' : 'secondary'}>
                  {lastCreated.role === 'admin' ? '管理员' : '普通用户'}
                </Badge>
                <Button size='sm' variant='ghost' onClick={() => copyCode(lastCreated.code)}>
                  <Copy className='h-4 w-4' />
                </Button>
              </div>
            </div>
            <p className='text-xs text-muted-foreground'>仅可使用 1 次，使用后自动失效</p>
            {lastCreated.role === 'admin' && (
              <p className='flex items-start gap-1.5 text-xs font-medium text-amber-700 dark:text-amber-400'>
                <AlertTriangle className='mt-0.5 h-3.5 w-3.5 shrink-0' />
                此码可创建管理员账号，请勿在公开渠道分享
              </p>
            )}
          </div>
        )}

        {/* 已生成的码列表 */}
        <div className='space-y-2'>
          {codesQuery.isLoading ? (
            <p className='py-4 text-center text-sm text-muted-foreground'>加载中...</p>
          ) : codes.length === 0 ? (
            <p className='py-4 text-center text-sm text-muted-foreground'>还没有邀请码</p>
          ) : (
            codes.map((item) => (
              <div
                key={item.code}
                className='flex items-center justify-between rounded-md border px-3 py-2 text-sm'
              >
                <div className='flex items-center gap-2'>
                  <span
                    className={
                      item.used
                        ? 'font-mono tracking-wider text-muted-foreground line-through'
                        : 'font-mono tracking-wider'
                    }
                  >
                    {item.code}
                  </span>
                  <Badge variant={item.role === 'admin' ? 'destructive' : 'secondary'}>
                    {item.role === 'admin' ? '管理员' : '普通用户'}
                  </Badge>
                  {item.used ? (
                    <span className='text-xs text-muted-foreground'>
                      已被 {item.used_by} 使用
                    </span>
                  ) : (
                    <span className='text-xs text-emerald-600 dark:text-emerald-400'>未使用</span>
                  )}
                </div>
                <Button
                  size='sm'
                  variant='ghost'
                  disabled={deleteMutation.isPending}
                  onClick={() => deleteMutation.mutate(item.code)}
                >
                  <Trash2 className='h-4 w-4' />
                </Button>
              </div>
            ))
          )}
        </div>

        <DialogFooter>
          <Button variant='outline' onClick={() => onOpenChange(false)}>
            关闭
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
