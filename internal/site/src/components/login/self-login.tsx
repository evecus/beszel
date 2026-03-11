/**
 * Self-login component for self-monitoring mode.
 *
 * - No -auth flag: auto-logins immediately, shows a brief loading spinner
 * - With -auth flag: shows a minimal single-password dialog (no email field)
 */
import { LoaderCircle, LockIcon, MonitorIcon } from "lucide-react"
import { useEffect, useState } from "react"
import { pb } from "@/lib/api"
import { $authenticated } from "@/lib/stores"
import { ModeToggle } from "../mode-toggle"
import { useTheme } from "../theme-provider"
import { Logo } from "../logo"

interface Props {
	requirePassword?: boolean
}

export default function SelfLoginPage({ requirePassword = false }: Props) {
	const { theme } = useTheme()
	const [password, setPassword] = useState("")
	const [error, setError] = useState("")
	const [loading, setLoading] = useState(!requirePassword)

	// No-auth mode: auto-login immediately
	useEffect(() => {
		if (!requirePassword) {
			autoLogin()
		}
	}, [requirePassword])

	async function autoLogin() {
		setLoading(true)
		try {
			const data = await pb.send("/api/beszel/self-autologin", {
				method: "POST",
			})
			if (data?.token && data?.record) {
				pb.authStore.save(data.token, data.record)
				$authenticated.set(true)
			}
		} catch {
			// If auto-login fails (user not ready yet), retry after delay
			setTimeout(autoLogin, 1500)
		} finally {
			setLoading(false)
		}
	}

	async function handlePasswordLogin(e: React.FormEvent) {
		e.preventDefault()
		setLoading(true)
		setError("")
		try {
			// In -auth mode, the account email is authUser@beszel.local
			// We don't know the username here, but the server auto-created
			// a single admin account. Use PocketBase's standard auth.
			// The frontend finds the account via identity = email which the
			// user doesn't know. So we call our dedicated endpoint that
			// validates only the password against the single self-mode account.
			const data = await pb.send("/api/beszel/self-autologin", {
				method: "POST",
				body: JSON.stringify({ password }),
			})
			if (data?.token && data?.record) {
				pb.authStore.save(data.token, data.record)
				$authenticated.set(true)
			} else {
				setError("Wrong password, please try again.")
			}
		} catch {
			setError("Wrong password, please try again.")
		} finally {
			setLoading(false)
		}
	}

	const borderColor = theme === "light" ? "hsl(30, 8%, 70%)" : "hsl(220, 3%, 25%)"

	// Loading / auto-login spinner
	if (!requirePassword) {
		return (
			<div className="min-h-svh grid items-center justify-center">
				<div className="flex flex-col items-center gap-4 text-muted-foreground">
					<Logo className="h-7 fill-foreground" />
					<LoaderCircle className="h-5 w-5 animate-spin" />
					<span className="text-sm">Connecting…</span>
				</div>
			</div>
		)
	}

	// Password dialog for -auth mode
	return (
		<div className="min-h-svh grid items-center py-12">
			<div
				className="grid gap-5 w-full px-4 mx-auto"
				// @ts-expect-error css var
				style={{ maxWidth: "21.5em", "--border": borderColor }}
			>
				<div className="absolute top-3 right-3">
					<ModeToggle />
				</div>

				<div className="text-center">
					<h1 className="mb-3">
						<Logo className="h-7 fill-foreground mx-auto" />
						<span className="sr-only">Beszel</span>
					</h1>
					<p className="text-sm text-muted-foreground flex items-center justify-center gap-1.5">
						<MonitorIcon className="h-3.5 w-3.5" />
						Self-monitoring
					</p>
				</div>

				<form onSubmit={handlePasswordLogin}>
					<div className="grid gap-2.5">
						<div className="relative">
							<LockIcon className="absolute left-3 top-3 h-4 w-4 text-muted-foreground" />
							<input
								type="password"
								placeholder="Password"
								value={password}
								onChange={(e) => {
									setPassword(e.target.value)
									setError("")
								}}
								required
								autoFocus
								autoComplete="current-password"
								className={[
									"flex h-10 w-full rounded-md border bg-background px-3 py-2 ps-9 text-sm",
									"ring-offset-background placeholder:text-muted-foreground",
									"focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
									"disabled:cursor-not-allowed disabled:opacity-50",
									error ? "border-red-500" : "border-input",
								].join(" ")}
								disabled={loading}
							/>
						</div>
						{error && <p className="text-xs text-red-500 px-1">{error}</p>}
						<button
							type="submit"
							disabled={loading || !password}
							className={[
								"inline-flex items-center justify-center rounded-md text-sm font-medium",
								"ring-offset-background transition-colors h-10 px-4 py-2",
								"bg-primary text-primary-foreground hover:bg-primary/90",
								"disabled:pointer-events-none disabled:opacity-50",
							].join(" ")}
						>
							{loading ? (
								<LoaderCircle className="h-4 w-4 animate-spin" />
							) : (
								"Enter"
							)}
						</button>
					</div>
				</form>
			</div>
		</div>
	)
}
