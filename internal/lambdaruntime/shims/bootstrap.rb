# doze-aws's Ruby runtime client: the Lambda Runtime API loop, in one file.
#
# It stands in for the aws_lambda_ric gem so a ruby3.x function runs with
# nothing installed but ruby. Handler resolution follows the gem:
# "file.method" or "file.Module::Class.method", called with the event and
# context keywords Ruby handlers take.

require "json"
require "net/http"
require "uri"

$stdout.sync = true
$stderr.sync = true

API = URI("http://#{ENV.fetch('AWS_LAMBDA_RUNTIME_API')}/2018-06-01/runtime/")
TASK_ROOT = ENV["LAMBDA_TASK_ROOT"] || Dir.pwd

class LambdaContext
  attr_reader :aws_request_id, :function_name, :function_version, :invoked_function_arn,
              :memory_limit_in_mb, :log_group_name, :log_stream_name, :deadline_ms,
              :client_context, :identity

  def initialize(request_id, headers)
    @aws_request_id = request_id
    @function_name = ENV["AWS_LAMBDA_FUNCTION_NAME"].to_s
    @function_version = ENV["AWS_LAMBDA_FUNCTION_VERSION"] || "$LATEST"
    @invoked_function_arn = headers["lambda-runtime-invoked-function-arn"].to_s
    @memory_limit_in_mb = (ENV["AWS_LAMBDA_FUNCTION_MEMORY_SIZE"] || "128").to_i
    @log_group_name = ENV["AWS_LAMBDA_LOG_GROUP_NAME"].to_s
    @log_stream_name = ENV["AWS_LAMBDA_LOG_STREAM_NAME"].to_s
    @deadline_ms = headers["lambda-runtime-deadline-ms"].to_i
    cc = headers["lambda-runtime-client-context"]
    @client_context = cc && !cc.empty? ? JSON.parse(cc) : nil
    ci = headers["lambda-runtime-cognito-identity"]
    @identity = ci && !ci.empty? ? JSON.parse(ci) : nil
  end

  def get_remaining_time_in_millis
    return 0 if @deadline_ms.zero?
    [@deadline_ms - (Time.now.to_f * 1000).to_i, 0].max
  end
end

def post(path, body, error_type = nil)
  req = Net::HTTP::Post.new(API.path + path)
  req["Content-Type"] = "application/json"
  req["Lambda-Runtime-Function-Error-Type"] = error_type if error_type
  req.body = body
  Net::HTTP.start(API.host, API.port) { |http| http.request(req) }
end

def error_body(e, request_id = nil)
  body = { "errorMessage" => e.message, "errorType" => e.class.name, "stackTrace" => e.backtrace || [] }
  body["requestId"] = request_id if request_id
  JSON.generate(body)
end

# "file.method" or "file.Mod::Klass.method" → a callable.
def load_handler(spec)
  raise ArgumentError, "Bad handler '#{spec}': not of the form file.method" unless spec.include?(".")
  file, target = spec.split(".", 2)
  $LOAD_PATH.unshift(TASK_ROOT) unless $LOAD_PATH.include?(TASK_ROOT)
  begin
    require File.join(TASK_ROOT, file)
  rescue LoadError => e
    raise LoadError, "Unable to load '#{file}': #{e.message}"
  end
  if target.include?(".")
    const_name, method_name = target.split(".", 2)
    receiver = Object.const_get(const_name)
  else
    receiver = Object
    method_name = target
  end
  unless receiver.respond_to?(method_name, true)
    raise NoMethodError, "Handler '#{method_name}' not found on #{receiver == Object ? file : receiver}"
  end
  ->(event, context) { receiver.send(method_name, event: event, context: context) }
end

spec = ARGV[0] || ENV["_HANDLER"].to_s
begin
  handler = load_handler(spec)
rescue Exception => e # rubocop:disable Lint/RescueException — every init failure is reported the same way
  puts "[ERROR] #{e.class}: #{e.message}"
  puts(e.backtrace || [])
  type = e.is_a?(LoadError) ? "Runtime.ImportModuleError" : "Runtime.HandlerNotFound"
  post("init/error", error_body(e), type)
  exit 1
end

loop do
  resp = Net::HTTP.start(API.host, API.port, read_timeout: nil) { |http| http.request(Net::HTTP::Get.new(API.path + "invocation/next")) }
  request_id = resp["lambda-runtime-aws-request-id"].to_s
  ENV["_X_AMZN_TRACE_ID"] = resp["lambda-runtime-trace-id"] if resp["lambda-runtime-trace-id"]
  body = resp.body.to_s
  event = begin
    body.empty? ? nil : JSON.parse(body)
  rescue JSON::ParserError
    body
  end
  context = LambdaContext.new(request_id, resp)
  begin
    result = handler.call(event, context)
    post("invocation/#{request_id}/response", JSON.generate(result))
  rescue Exception => e # rubocop:disable Lint/RescueException — a handler may raise anything
    puts "[ERROR] #{e.class}: #{e.message}"
    puts(e.backtrace || [])
    post("invocation/#{request_id}/error", error_body(e, request_id), e.class.name)
  ensure
    $stdout.flush
    $stderr.flush
  end
end
