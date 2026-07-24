export class UnsupportedAsyncLifecycleError extends Error {
  constructor(stage: string) {
    super(`${stage} returned a Promise/thenable; gossr lifecycle hooks must be synchronous`)
    this.name = 'UnsupportedAsyncLifecycleError'
  }
}

export class GossrCleanupError extends Error {
  readonly errors: readonly unknown[]

  constructor(errors: readonly unknown[]) {
    super(`gossr cleanup failed in ${errors.length} step${errors.length === 1 ? '' : 's'}`)
    this.name = 'GossrCleanupError'
    this.errors = errors
  }
}

export function isThenable(value: unknown): value is PromiseLike<unknown> {
  return (
    (typeof value === 'object' && value !== null)
    || typeof value === 'function'
  ) && typeof (value as { then?: unknown }).then === 'function'
}

export function consumeThenableRejection(value: PromiseLike<unknown>) {
  // Hooks are deliberately synchronous, but rejected async returns must still
  // be observed or browsers and SSR runtimes will emit a second unhandled
  // rejection after the controlled lifecycle error.
  void Promise.resolve(value).catch(() => {})
}

export class SynchronousDisposableScope {
  private readonly disposers: Array<() => void> = []
  private accepting = true
  private disposed = false

  onDispose = (disposer: () => void) => {
    if (!this.accepting)
      throw new Error('onDispose() is only available while setup() is running')
    if (typeof disposer !== 'function')
      throw new TypeError('onDispose() requires a function')
    this.disposers.push(disposer)
  }

  finishSetup() {
    this.accepting = false
  }

  disposeInto(errors: unknown[]) {
    if (this.disposed)
      return

    this.accepting = false
    this.disposed = true
    for (let index = this.disposers.length - 1; index >= 0; index -= 1) {
      try {
        const result: unknown = this.disposers[index]()
        if (isThenable(result)) {
          consumeThenableRejection(result)
          throw new UnsupportedAsyncLifecycleError('A disposer')
        }
      }
      catch (error) {
        errors.push(error)
      }
    }
    this.disposers.length = 0
  }
}

export function cleanupError(errors: unknown[]) {
  if (errors.length === 0)
    return undefined
  if (errors.length === 1 && errors[0] instanceof Error)
    return errors[0]
  return new GossrCleanupError(errors)
}

export function attachCleanupError(primary: unknown, secondary: unknown) {
  if (!secondary || (typeof primary !== 'object' && typeof primary !== 'function') || primary === null)
    return

  try {
    Object.defineProperty(primary, 'gossrCleanupError', {
      configurable: true,
      enumerable: false,
      value: secondary,
    })
  }
  catch {
    // The original failure remains authoritative even if it is not extensible.
  }
}
