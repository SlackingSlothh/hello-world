"use client";

import { zodResolver } from "@hookform/resolvers/zod";
import { useForm } from "react-hook-form";
import { z } from "zod";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

const loginSchema = z.object({
  username: z.string().min(1, "Enter your username or email."),
  password: z.string().min(1, "Enter your password."),
});

const registerSchema = z
  .object({
    username: z.string().trim().min(1, "Enter a username.").max(100),
    email: z.email("Enter a valid email address."),
    password: z.string().min(12, "Password must be at least 12 characters."),
    confirmPassword: z.string().min(1, "Confirm your password."),
  })
  .refine((value) => value.password === value.confirmPassword, {
    message: "Passwords do not match.",
    path: ["confirmPassword"],
  });

type LoginValues = z.infer<typeof loginSchema>;
type RegisterValues = z.infer<typeof registerSchema>;
type AuthFormValues = LoginValues | RegisterValues;

export function AuthForm({
  mode,
  onSubmit,
  error,
  sessionExpired = false,
  retryAfter,
  submitting = false,
}: {
  mode: "login" | "register";
  onSubmit: (values: AuthFormValues) => void | Promise<void>;
  error?: string;
  sessionExpired?: boolean;
  retryAfter?: string;
  submitting?: boolean;
}) {
  const isRegister = mode === "register";
  const form = useForm<AuthFormValues>({
    resolver: zodResolver(isRegister ? registerSchema : loginSchema),
    defaultValues: isRegister
      ? { username: "", email: "", password: "", confirmPassword: "" }
      : { username: "", password: "" },
  });
  const title = isRegister ? "Create your account" : "Sign in";

  return (
    <form
      className="mx-auto w-full max-w-md space-y-5 rounded-xl border bg-background p-6 shadow-sm sm:p-8"
      noValidate
      onSubmit={form.handleSubmit((values) => onSubmit(values))}
    >
      <header>
        <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
        <p className="mt-2 text-sm text-muted-foreground">
          {isRegister
            ? "New accounts need to be added to a group before database access is granted."
            : "Enter your account credentials to continue."}
        </p>
      </header>

      {sessionExpired ? (
        <p className="rounded-md border bg-muted p-3 text-sm" role="status">
          Your session expired. Please sign in again.
        </p>
      ) : null}
      {error ? (
        <div className="rounded-md border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive" role="alert">
          {error}
          {retryAfter ? ` Try again ${retryAfter}.` : ""}
        </div>
      ) : null}

      <Field
        autoComplete="username"
        error={form.formState.errors.username?.message}
        id="username"
        label="Username"
        registration={form.register("username")}
      />
      {isRegister ? (
        <Field
          autoComplete="email"
          error={"email" in form.formState.errors ? form.formState.errors.email?.message : undefined}
          id="email"
          label="Email"
          registration={form.register("email")}
          type="email"
        />
      ) : null}
      <Field
        autoComplete={isRegister ? "new-password" : "current-password"}
        error={form.formState.errors.password?.message}
        id="password"
        label="Password"
        registration={form.register("password")}
        type="password"
      />
      {isRegister ? (
        <Field
          autoComplete="new-password"
          error={
            "confirmPassword" in form.formState.errors
              ? form.formState.errors.confirmPassword?.message
              : undefined
          }
          id="confirmPassword"
          label="Confirm password"
          registration={form.register("confirmPassword")}
          type="password"
        />
      ) : null}

      <Button className="w-full" disabled={submitting} type="submit">
        {submitting ? "Please wait…" : isRegister ? "Create account" : "Sign in"}
      </Button>
    </form>
  );
}

function Field({
  id,
  label,
  type = "text",
  autoComplete,
  error,
  registration,
}: {
  id: string;
  label: string;
  type?: string;
  autoComplete?: string;
  error?: string;
  registration: ReturnType<ReturnType<typeof useForm<AuthFormValues>>["register"]>;
}) {
  return (
    <div className="space-y-2">
      <Label htmlFor={id}>{label}</Label>
      <Input
        aria-describedby={error ? `${id}-error` : undefined}
        aria-invalid={Boolean(error)}
        autoComplete={autoComplete}
        id={id}
        type={type}
        {...registration}
      />
      {error ? (
        <p className="text-sm text-destructive" id={`${id}-error`}>
          {error}
        </p>
      ) : null}
    </div>
  );
}
