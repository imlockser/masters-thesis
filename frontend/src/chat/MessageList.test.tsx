import { renderToStaticMarkup } from 'react-dom/server'
import { expect, it } from 'vitest'
import { MessageList } from './MessageList'

function renderMessage(content: string) {
  return renderToStaticMarkup(
    <MessageList
      chat={{
        id: 'chat', user_id: 'user', title: 'Forecast', created_at: '',
        messages: [{
          id: 1, chat_id: 'chat', role: 'assistant', created_at: '',
          payload: { content },
        }],
      }}
      run={null}
    />,
  )
}

it('renders table line breaks while preserving formatting and alignment', () => {
  const markup = renderMessage('| Forecast |\n| :---: |\n| **101750 MWh** per day<br>Forecast details<br/>[More](https://example.com)<BR />02:00–14:00 UTC |')
  const cell = markup.match(/<td[^>]*>(.*?)<\/td>/s)?.[1]
  expect(cell?.match(/<br\s*\/>/g)).toHaveLength(3)
  expect(cell).toContain('<strong>101750 MWh</strong>')
  expect(cell).toContain('<a href="https://example.com">More</a>')
  expect(cell).not.toMatch(/^<br/)
  expect(markup).toContain('text-align:center')
})

it('renders newline characters in text and cells without requiring bullets', () => {
  const markup = renderMessage('First line\n**Second line**\nThird line\n\n| Forecast |\n| --- |\n| First sentence&#10;Second sentence |')
  expect(markup).toContain('<p>First line<br/>\n<strong>Second line</strong><br/>\nThird line</p>')
  const cell = markup.match(/<td[^>]*>(.*?)<\/td>/s)?.[1]
  expect(cell).toContain('First sentence<br/>\nSecond sentence')
})

it('preserves inline bullets and code and keeps other HTML disabled', () => {
  const markup = renderMessage('• First • Second\n\n| Value |\n| --- |\n| **• One • Two** and `literal <br> text` |\n\n```text\nfirst\nsecond <br>\n```\n\n<img src="x" onerror="alert(1)">')
  expect(markup).toContain('• First • Second')
  expect(markup).toContain('<strong>• One • Two</strong>')
  expect(markup).toContain('<code>literal &lt;br&gt; text</code>')
  expect(markup).toContain('first\nsecond &lt;br&gt;\n</code>')
  expect(markup).not.toContain('<br/>')
  expect(markup).not.toContain('<img')
  expect(markup).not.toContain('onerror')
})
