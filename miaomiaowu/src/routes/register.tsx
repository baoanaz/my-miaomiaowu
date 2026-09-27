// @ts-nocheck
import { useEffect, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useMutation, useQuery } from '@tanstack/react-query'
import { createFileRoute, Link, redirect, useNavigate } from '@tanstack/react-router'
import { toast } from 'sonner'
import { ArrowLeft, Ticket } from 'lucide-react'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Button } from '@/components/ui/button'
import { handleServerError } from '@/lib/handle-server-error'

// 用户自助注册（本仓库对上游的增量功能）。
// 入口在登录页底部；注册必须凭邀请码，邀请码首位决定角色（A=管理员 / B=普通用户）。
export const Route = createFileRoute('/register')({
  beforeLoad: () => {
    const token = useAuthStore.getState().auth.accessToken
    if (token) {
      throw redirect({ to: '/' })
    }
  },
  component: RegisterPage,
})

type RegisterFormValues = {
  username: string
  password: string
  confirm_password: string
  email: string
  invite_code: string
}

function RegisterPage() {
  return <RegisterView />
}

function RegisterView() {
  const navigate = useNavigate()
  const [turnstileToken, setTurnstileToken] = useState('')

  // 注册是否开放 + 验证码配置
  const { data: status, isLoading } = useQuery({
    queryKey: ['register-status'],
    queryFn: async () => {
      const response = await api.get('/api/register/status')
      return response.data as {
        enabled: boolean
        invite_required: boolean
        captcha_enabled: boolean
        captcha_site_key?: string
      }
    },
    staleTime: 0,
  })

  const form = useForm<RegisterFormValues>({
    defaultValues: {
      username: '',
      password: '',
      confirm_password: '',
      email: '',
      invite_code: '',
    },
  })

  const register = useMutation({
    mutationFn: async (values: RegisterFormValues) => {
      const response = await api.post('/api/register', {
        username: values.username.trim(),
        password: values.password,
        email: values.email.trim(),
        invite_code: values.invite_code.trim().toUpperCase(),
        turnstile_token: turnstileToken,
      })
      return response.data as { username: string; message: string }
    },
    onSuccess: (data) => {
      toast.success(data?.message || '注册成功，请登录')
      navigate({ to: '/login' })
    },
    onError: (error) => {
      handleServerError(error)
    },
  })

  const onSubmit = form.handleSubmit((values) => {
    if (values.password !== values.confirm_password) {
      toast.error('两次输入的密码不一致')
      return
    }
    register.mutate(values)
  })

  return (
    <div className='login-pixel-bg flex min-h-svh items-center justify-center px-4 py-12'>
      <Card className='w-full max-w-sm shadow-lg'>
        <CardHeader className='space-y-2 text-center'>
          <CardTitle className='text-2xl font-semibold'>注册妙妙屋</CardTitle>
          <CardDescription>
            {isLoading ? '正在检查注册状态' : '请填写邀请码完成注册'}
          </CardDescription>
        </CardHeader>
        <CardContent>
          {isLoading ? (
            <p className='py-8 text-center text-sm text-muted-foreground'>加载中...</p>
          ) : !status?.enabled ? (
            <div className='space-y-4 py-4 text-center'>
              <p className='text-sm text-muted-foreground'>
                当前未开放注册，请联系管理员获取账号。
              </p>
              <Button asChild variant='outline' className='w-full'>
                <Link to='/login'>
                  <ArrowLeft className='mr-2 h-4 w-4' />
                  返回登录
                </Link>
              </Button>
            </div>
          ) : (
            <form className='space-y-5' onSubmit={onSubmit}>
              <div className='space-y-2'>
                <Label htmlFor='invite_code'>邀请码</Label>
                <div className='relative'>
                  <Ticket className='absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground' />
                  <Input
                    id='invite_code'
                    className='pl-9 font-mono uppercase tracking-wider'
                    autoCapitalize='characters'
                    autoComplete='off'
                    placeholder='例如 A7K2M9'
                    maxLength={6}
                    {...form.register('invite_code', { required: true })}
                  />
                </div>
                <p className='text-xs text-muted-foreground'>
                  邀请码由管理员发放；A 开头为管理员码，B 开头为普通用户码。
                </p>
              </div>

              <div className='space-y-2'>
                <Label htmlFor='username'>用户名</Label>
                <Input
                  id='username'
                  autoCapitalize='none'
                  autoComplete='username'
                  placeholder='3-32 位字母、数字、下划线'
                  {...form.register('username', { required: true })}
                />
              </div>

              <div className='space-y-2'>
                <Label htmlFor='password'>密码</Label>
                <Input
                  id='password'
                  type='password'
                  autoComplete='new-password'
                  placeholder='至少 8 位'
                  {...form.register('password', { required: true })}
                />
              </div>

              <div className='space-y-2'>
                <Label htmlFor='confirm_password'>确认密码</Label>
                <Input
                  id='confirm_password'
                  type='password'
                  autoComplete='new-password'
                  placeholder='再次输入密码'
                  {...form.register('confirm_password', { required: true })}
                />
              </div>

              <div className='space-y-2'>
                <Label htmlFor='email'>邮箱（可选）</Label>
                <Input
                  id='email'
                  type='email'
                  autoComplete='email'
                  placeholder='用于找回账号'
                  {...form.register('email')}
                />
              </div>

              {status.captcha_enabled && status.captcha_site_key && (
                <TurnstileWidget siteKey={status.captcha_site_key} onToken={setTurnstileToken} />
              )}

              <Button type='submit' className='w-full' disabled={register.isPending}>
                {register.isPending ? '注册中...' : '注册'}
              </Button>

              <Button asChild variant='ghost' className='w-full'>
                <Link to='/login'>
                  <ArrowLeft className='mr-2 h-4 w-4' />
                  已有账号，返回登录
                </Link>
              </Button>
            </form>
          )}
        </CardContent>
      </Card>
    </div>
  )
}

// 与登录页同款 Cloudflare Turnstile 挂件
function TurnstileWidget({ siteKey, onToken }: { siteKey: string; onToken: (token: string) => void }) {
  const containerRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const render = () => {
      const turnstile = (window as any).turnstile
      if (turnstile && containerRef.current) {
        containerRef.current.innerHTML = ''
        turnstile.render(containerRef.current, {
          sitekey: siteKey,
          callback: onToken,
          'expired-callback': () => onToken(''),
          'error-callback': () => onToken(''),
        })
      }
    }
    const existing = document.querySelector('script[data-turnstile]') as HTMLScriptElement | null
    if (existing) {
      if ((window as any).turnstile) render()
      return
    }
    const script = document.createElement('script')
    script.src = 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit'
    script.async = true
    script.defer = true
    script.dataset.turnstile = 'true'
    script.onload = render
    document.head.appendChild(script)
  }, [siteKey, onToken])

  return <div ref={containerRef} className='flex justify-center' />
}
