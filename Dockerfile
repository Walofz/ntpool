# Build Stage
FROM node:20-alpine AS builder
WORKDIR /app

# Install dependencies
COPY package*.json ./
RUN npm ci

# Copy source code and configuration
COPY tsconfig.json ./
COPY src ./src

# Build TypeScript to JavaScript
RUN npm run build

# Production Stage
FROM node:20-alpine AS runner
WORKDIR /app

ENV NODE_ENV=production

# Install production dependencies only
COPY package*.json ./
RUN npm ci --only=production

# Copy compiled app and static assets
COPY --from=builder /app/dist ./dist
COPY public ./public
COPY .env.example ./.env.example
COPY .env ./.env

# Stratum TCP Port (3333) & Web Dashboard HTTP Port (8080)
EXPOSE 3333 8080

# Run ntpool
CMD ["node", "dist/index.js"]
