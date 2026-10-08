type AttachmentTransportKey = {
  sessionId: string
  attachmentId: string
}

function keyFor(input: AttachmentTransportKey): string {
  return `${input.sessionId}:${input.attachmentId}`
}

class AttachmentTransportCoordinator {
  private readonly tails = new Map<string, Promise<void>>()

  async runMany<T>(inputs: AttachmentTransportKey[], operation: () => Promise<T> | T): Promise<T> {
    const unique = [...new Map(inputs.map((input) => [keyFor(input), input])).values()]
      .sort((left, right) => keyFor(left).localeCompare(keyFor(right)))
    const runAt = async (index: number): Promise<T> => {
      if (index === unique.length) return await operation()
      const input = unique[index]
      if (input == null) return await operation()
      return await this.run(input, async () => await runAt(index + 1))
    }
    return await runAt(0)
  }

  async run<T>(input: AttachmentTransportKey, operation: () => Promise<T> | T): Promise<T> {
    const key = keyFor(input)
    const previous = this.tails.get(key) ?? Promise.resolve()
    let release: () => void = () => undefined
    const current = new Promise<void>((resolve) => {
      release = resolve
    })
    const tail = previous.catch(() => undefined).then(() => current)
    this.tails.set(key, tail)

    await previous.catch(() => undefined)
    try {
      return await operation()
    } finally {
      release()
      if (this.tails.get(key) === tail) this.tails.delete(key)
    }
  }
}

export const attachmentTransportCoordinator = new AttachmentTransportCoordinator()
