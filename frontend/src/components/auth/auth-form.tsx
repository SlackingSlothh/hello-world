"use client";

import { zodResolver } from "@hookform/resolvers/zod";
import { useForm } from "react-hook-form";
import { z } from "zod";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

const loginSchema = z.object({
  username: z.string().min(1, "Enter your username."),
  password: z.string().min(1, "Enter your password."),
});

export type LoginFormValues = z.infer<typeof loginSchema>;

export function AuthForm({
  onSubmit,
  error,
  sessionExpired = false,
  submitting = false,
}: {
  onSubmit: (values: LoginFormValues) => void | Promise<void>;
  error?: string;
  sessionExpired?: boolean;
  submitting?: boolean;
}) {
  const form = useForm<LoginFormValues>({
    resolver: zodResolver(loginSchema),
    defaultValues: { username: "", password: "" },
  });

  return (
    <form
      className="mx-auto w-full max-w-md space-y-5 rounded-xl border bg-background p-6 shadow-sm sm:p-8"
      noValidate
      onSubmit={form.handleSubmit((values) => onSubmit(values))}
    >
      <header>
        <h1 className="text-2xl font-semibold tracking-tight">Sign in</h1>
        <p className="mt-2 text-sm text-muted-foreground">
          Enter your account credentials to continue.
        </p>
      </header>

      {sessionExpired ? (
        <p className="rounded-md border bg-muted p-3 text-sm" role="status">
          Your session expired. Please sign in again.
        </p>
      ) : null}
      {error ? (
        <div
          className="rounded-md border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive"
          role="alert"
        >
          {error}
        </div>
      ) : null}

      <Field
        autoComplete="username"
        error={form.formState.errors.username?.message}
        id="username"
        label="Username"
        registration={form.register("username")}
      />
      <Field
        autoComplete="current-password"
        error={form.formState.errors.password?.message}
        id="password"
        label="Password"
        registration={form.register("password")}
        type="password"
      />

      <Button className="w-full" disabled={submitting} type="submit">
        {submitting ? "Signing in…" : "Sign in"}
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
  registration: ReturnType<
    ReturnType<typeof useForm<LoginFormValues>>["register"]
  >;
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
