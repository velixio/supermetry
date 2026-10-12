# frozen_string_literal: true

$LOAD_PATH.unshift File.expand_path("../lib", __dir__)
require "supermetry"
require "minitest/autorun"
require "socket"
require "json"

# Minimal HTTP/1.1 server recording requests and answering with queued statuses.
class StubServer
  Recorded = Struct.new(:method, :path, :headers, :body, keyword_init: true)

  attr_reader :requests, :port

  def initialize(statuses = [])
    @statuses = statuses.dup
    @requests = []
    @lock = Mutex.new
    @server = TCPServer.new("127.0.0.1", 0)
    @port = @server.addr[1]
    @thread = Thread.new { serve }
    @thread.report_on_exception = false
  end

  def base_url
    "http://127.0.0.1:#{@port}"
  end

  def stop
    @thread.kill
    @server.close
  rescue IOError
    nil
  end

  def recorded
    @lock.synchronize { @requests.dup }
  end

  private

  def serve
    loop do
      socket = @server.accept
      handle(socket)
    end
  rescue IOError, Errno::EBADF
    nil
  end

  def handle(socket)
    request_line = socket.gets
    return socket.close if request_line.nil?

    method, path, = request_line.split(" ")
    headers = {}
    while (line = socket.gets) && line != "\r\n"
      key, value = line.chomp.split(": ", 2)
      headers[key.downcase] = value
    end
    body = socket.read(headers.fetch("content-length", "0").to_i)
    status = @lock.synchronize do
      @requests << Recorded.new(method: method, path: path, headers: headers, body: JSON.parse(body))
      @statuses.shift || 202
    end
    extra = status == 429 ? "Retry-After: 0\r\n" : ""
    socket.write("HTTP/1.1 #{status} X\r\n#{extra}Content-Length: 0\r\nConnection: close\r\n\r\n")
  ensure
    socket.close
  end
end
